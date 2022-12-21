//go:generate ../../../tools/readme_config_includer/generator
package alerta

import (
	_ "embed"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/influxdata/telegraf"
	"github.com/influxdata/telegraf/config"
	"github.com/influxdata/telegraf/plugins/common/tls"
	"github.com/influxdata/telegraf/plugins/inputs"
)

//go:embed sample.conf
var sampleConfig string

type AlertaStats struct {
	Version string         `json:"version"`
	Uptime  int64          `json:"uptime"`
	Met     []AlertaMetric `json:"metrics"`
}

type AlertaMetric struct {
	Group     string `json:"group"`
	Name      string `json:"name"`      // Action
	Type      string `json:"type"`      // Status type
	Value     int64  `json:"value"`     // Value for gauge type
	Count     int64  `json:"count"`     // Count for timer type
	TotalTime int64  `json:"totalTime"` // Total time used to perform action for timer type
}

type Alerta struct {
	Urls            []string
	ResponseTimeout config.Duration
	tls.ClientConfig

	Headers map[string]string `toml:"headers"`

	// HTTP Basic Auth Credentials
	Username config.Secret `toml:"username"`
	Password config.Secret `toml:"password"`

	// Absolute path to file with Bearer token
	ApiKey config.Secret `toml:"api_key"`

	// HTTP client
	client *http.Client
}

func (*Alerta) SampleConfig() string {
	return sampleConfig
}

func (a *Alerta) Gather(acc telegraf.Accumulator) error {
	var wg sync.WaitGroup

	// Create an HTTP client that is re-used for each
	// collection interval
	if a.client == nil {
		client, err := a.createHTTPClient()
		if err != nil {
			return err
		}
		a.client = client
	}

	for _, u := range a.Urls {
		addr, err := url.Parse(u)
		if err != nil {
			acc.AddError(fmt.Errorf("unable to parse address '%s': %s", u, err))
			continue
		}

		if addr.Path != "/management/status" {
			acc.AddError(fmt.Errorf("excpeted '/management/status' at the end of url:' '%s'", u))
			continue
		}

		wg.Add(1)
		go func(addr *url.URL) {
			defer wg.Done()
			acc.AddError(a.gatherURL(addr, acc))
		}(addr)
	}

	wg.Wait()
	return nil
}

func (a *Alerta) createHTTPClient() (*http.Client, error) {
	tlsCfg, err := a.ClientConfig.TLSConfig()
	if err != nil {
		return nil, err
	}

	if a.ResponseTimeout < config.Duration(time.Second) {
		a.ResponseTimeout = config.Duration(time.Second * 5)
	}

	client := &http.Client{
		Transport: &http.Transport{
			TLSClientConfig: tlsCfg,
		},
		Timeout: time.Duration(a.ResponseTimeout),
	}

	return client, nil
}

func (a *Alerta) gatherURL(addr *url.URL, acc telegraf.Accumulator) error {
	resp, err := a.client.Get(addr.String())
	if err != nil {
		return fmt.Errorf("error making HTTP request to %s: %s", addr.String(), err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("%s returned HTTP status %s", addr.String(), resp.Status)
	}
	var body []byte
	contentType := strings.Split(resp.Header.Get("Content-Type"), ";")[0]
	if contentType == "application/json" {
		body, err = io.ReadAll(resp.Body)
		if err != nil {
			return fmt.Errorf("failed to read body: %s", err)
		}
	} else {
		return fmt.Errorf("%s returned unexpected content type %s", addr, contentType)
	}

	var stats = &AlertaStats{}
	json.Unmarshal(body, stats)

	if len(stats.Version) == 0 {
		return fmt.Errorf("expected version in response: %s", addr)
	}

	tags := map[string]string{
		"url":     addr.String(),
		"version": stats.Version,
	}

	fields := map[string]interface{}{
		"uptime": stats.Uptime,
	}
	var fieldName string
	for _, m := range stats.Met {
		fieldName = ""
		if m.Group != "alerts" {
			continue
		}
		if m.Type == "timer" {
			fieldName = m.Name + "_" + m.Group + "_time"
			fields[fieldName] = m.TotalTime

			fieldName = m.Name + "_" + m.Group
			fields[fieldName] = m.Count
		} else if m.Type == "gauge" {
			fieldName = m.Name + "_" + m.Group
			fields[fieldName] = m.Value
		}
	}

	acc.AddFields(
		"alerta",
		fields,
		tags,
	)

	return nil
}

func init() {
	inputs.Add("alerta", func() telegraf.Input {
		return &Alerta{}
	})
}
