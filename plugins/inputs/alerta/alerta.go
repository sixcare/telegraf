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
	Group string `json:"group"`
	Name  string `json:"name"`
	Type  string `json:"type"`
	Value int64  `json:"value"`
	Count int64  `json:"count"`
}

type Alerta struct {
	Urls            []string
	ResponseTimeout config.Duration
	tls.ClientConfig

	// HTTP client
	client *http.Client
}

func (*Alerta) SampleConfig() string {
	return sampleConfig
}

func (n *Alerta) Gather(acc telegraf.Accumulator) error {
	var wg sync.WaitGroup

	// Create an HTTP client that is re-used for each
	// collection interval
	if n.client == nil {
		client, err := n.createHTTPClient()
		if err != nil {
			return err
		}
		n.client = client
	}

	for _, u := range n.Urls {
		addr, err := url.Parse(u)
		if err != nil {
			acc.AddError(fmt.Errorf("unable to parse address '%s': %s", u, err))
			continue
		}

		wg.Add(1)
		go func(addr *url.URL) {
			defer wg.Done()
			acc.AddError(n.gatherURL(addr, acc))
		}(addr)
	}

	wg.Wait()
	return nil
}

func (n *Alerta) createHTTPClient() (*http.Client, error) {
	tlsCfg, err := n.ClientConfig.TLSConfig()
	if err != nil {
		return nil, err
	}

	if n.ResponseTimeout < config.Duration(time.Second) {
		n.ResponseTimeout = config.Duration(time.Second * 5)
	}

	client := &http.Client{
		Transport: &http.Transport{
			TLSClientConfig: tlsCfg,
		},
		Timeout: time.Duration(n.ResponseTimeout),
	}

	return client, nil
}

func (n *Alerta) gatherURL(addr *url.URL, acc telegraf.Accumulator) error {
	resp, err := n.client.Get(addr.String())
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

	tags := map[string]string{
		"url":     addr.String(),
		"version": stats.Version,
	}

	fields := map[string]interface{}{
		"uptime": stats.Uptime,
	}

	for _, m := range stats.Met {
		if m.Group != "alerts" {
			continue
		}
		fieldName := m.Name + "_" + m.Group
		if fieldName == "total_alerts" {
			m.Count = m.Value
		}
		fields[fieldName] = m.Count
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
