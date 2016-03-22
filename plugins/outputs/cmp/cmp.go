package cmp

import (
	"bytes"
	"crypto/tls"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/influxdata/telegraf"
	"github.com/influxdata/telegraf/internal"
	"github.com/influxdata/telegraf/plugins/outputs"
)

type Cmp struct {
	ApiUser     string
	ApiKey      string
	ResourceId  string
	CmpInstance string
	Timeout     internal.Duration

	client *http.Client
}

var sampleConfig = `
  # Cmp Api User and Key
  api_user = "my-api-user" # required.
  api_key = "my-api-key" # required.
  resource_id = "1234"

  # Cmp Instance URL
  cmp_instance = "https://yourcmpinstance" # required

  # Connection timeout.
  # timeout = "5s"
`

var translateMap = map[string]Translation{
	"cpu-usage.user": {
		Name: "cpu-usage.user",
		Unit: "percent",
	},
	"cpu-usage.system": {
		Name: "cpu-usage.system",
		Unit: "percent",
	},
	"mem-available.percent": {
		Name:       "memory-usage",
		Unit:       "percent",
		Conversion: memory_used_from_available,
	},
	"system-load1": {
		Name: "load-avg-1",
	},
	"system-load5": {
		Name: "load-avg-5",
	},
	"system-load15": {
		Name: "load-avg-15",
	},
	"disk-used.percent": {
		Name: "disk-usage",
	},
	//     "system-uptime": {
	//         Name: "uptime",
	//     },
	"docker_cpu-usage.percent": {
		Name: "docker-cpu-usage.system",
		Unit: "percent",
	},
	"docker_mem-usage.percent": {
		Name: "docker-memory-usage",
		Unit: "percent",
	},
}

type Translation struct {
	Name       string
	Unit       string
	Conversion func(interface{}) interface{}
}

func memory_used_from_available(available interface{}) interface{} {
	return (100.0 - available.(float64))
}

type CmpData struct {
	MonitoringSystem string      `json:"monitoring_system"`
	ResourceId       string      `json:"resource_id"`
	Metrics          []CmpMetric `json:"metrics"`
}

type CmpMetric struct {
	Metric string    `json:"metric"`
	Unit   string    `json:"unit"`
	Value  float64   `json:"value"`
	Time   time.Time `json:"time"`
}

func (data *CmpData) AddMetric(item CmpMetric) []CmpMetric {
	data.Metrics = append(data.Metrics, item)
	return data.Metrics
}

func (a *Cmp) Connect() error {
	if a.ApiUser == "" || a.ApiKey == "" || a.CmpInstance == "" || a.ResourceId == "" {
		return fmt.Errorf("api_user, api_key, resource_id and cmp_instance are required fields for cmp output")
	}
	tr := &http.Transport{
		TLSClientConfig: &tls.Config{InsecureSkipVerify: true},
	}

	a.client = &http.Client{
		Transport: tr,
		Timeout:   a.Timeout.Duration,
	}
	return nil
}

func (a *Cmp) Write(metrics []telegraf.Metric) error {
	if len(metrics) == 0 {
		return nil
	}
	cmp_data := &CmpData{
		MonitoringSystem: "telegraf",
		ResourceId:       a.ResourceId,
	}

	for _, m := range metrics {

		suffix := ""
		cpu := m.Tags()["cpu"]
		path := m.Tags()["path"]
		container_name := m.Tags()["cont_name"]

		if len(cpu) > 0 && cpu != "cpu-total" {
			suffix = cpu[3:]
		} else if len(path) > 0 {
			suffix = path
		} else if len(container_name) > 0 {
			suffix = container_name
		}

		timestamp := m.Time()
		for k, v := range m.Fields() {
			metric_name := m.Name() + "-" + strings.Replace(k, "_", ".", -1)
			translation, found := translateMap[metric_name]
			if found {
				cmp_name := translation.Name
				if len(suffix) > 0 {
					cmp_name += "." + suffix
				}

				conversion := translation.Conversion
				if conversion != nil {
					v = conversion(v)
				}

				cmp_data.AddMetric(CmpMetric{
					Metric: cmp_name,
					Unit:   translation.Unit,
					Value:  v.(float64),
					Time:   timestamp,
				})
			}
		}
	}

	cmp_bytes, err := json.Marshal(cmp_data)
	if err != nil {
		return fmt.Errorf("unable to marshal TimeSeries, %s\n", err.Error())
	}
	req, err := http.NewRequest("POST", a.authenticatedUrl(), bytes.NewBuffer(cmp_bytes))
	if err != nil {
		return fmt.Errorf("unable to create http.Request, %s\n", err.Error())
	}
	req.Header.Add("Content-Type", "application/json")
	req.SetBasicAuth(a.ApiUser, a.ApiKey)

	resp, err := a.client.Do(req)
	if err != nil {
		return fmt.Errorf("error POSTing metrics, %s\n", err.Error())
	}
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode > 209 {
		return fmt.Errorf("received bad status code, %d\n", resp.StatusCode)
	}

	return nil
}

func (a *Cmp) SampleConfig() string {
	return sampleConfig
}

func (a *Cmp) Description() string {
	return "Configuration for Cmp Server to send metrics to."
}

func (a *Cmp) authenticatedUrl() string {

	return fmt.Sprintf("%s/metrics", a.CmpInstance)
}

func (a *Cmp) Close() error {
	return nil
}

func init() {
	outputs.Add("cmp", func() telegraf.Output {
		return &Cmp{}
	})
}
