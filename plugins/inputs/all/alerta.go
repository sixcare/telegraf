//go:build !custom || inputs || inputs.alerta

package all

import _ "github.com/influxdata/telegraf/plugins/inputs/alerta" // register plugin
