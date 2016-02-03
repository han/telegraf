package kafka

import (
	"crypto/tls"
	"errors"
	"fmt"

	"github.com/influxdata/telegraf"
	"github.com/influxdata/telegraf/internal"
	"github.com/influxdata/telegraf/plugins/outputs"

	"github.com/Shopify/sarama"
)

type Kafka struct {
	// Kafka brokers to send metrics to
	Brokers []string
	// Kafka topic
	Topic string
	// Routing Key Tag
	RoutingTag string `toml:"routing_tag"`

	// Legacy SSL config options
	// TLS client certificate
	Certificate string
	// TLS client key
	Key string
	// TLS certificate authority
	CA string

	// Path to CA file
	SSLCA string `toml:"ssl_ca"`
	// Path to host cert file
	SSLCert string `toml:"ssl_cert"`
	// Path to cert key file
	SSLKey string `toml:"ssl_key"`

	// Skip SSL verification
	InsecureSkipVerify bool

	tlsConfig tls.Config
	producer  sarama.SyncProducer
}

var sampleConfig = `
  ### URLs of kafka brokers
  brokers = ["localhost:9092"]
  ### Kafka topic for producer messages
  topic = "telegraf"
  ### Telegraf tag to use as a routing key
  ###  ie, if this tag exists, it's value will be used as the routing key
  routing_tag = "host"

  ### Optional SSL Config
  # ssl_ca = "/etc/telegraf/ca.pem"
  # ssl_cert = "/etc/telegraf/cert.pem"
  # ssl_key = "/etc/telegraf/key.pem"
  ### Use SSL but skip chain & host verification
  # insecure_skip_verify = false
`

func (k *Kafka) Connect() error {
	config := sarama.NewConfig()
	// Wait for all in-sync replicas to ack the message
	config.Producer.RequiredAcks = sarama.WaitForAll
	// Retry up to 10 times to produce the message
	config.Producer.Retry.Max = 10

	// Legacy support ssl config
	if k.Certificate != "" {
		k.SSLCert = k.Certificate
		k.SSLCA = k.CA
		k.SSLKey = k.Key
	}

	tlsConfig, err := internal.GetTLSConfig(
		k.SSLCert, k.SSLKey, k.SSLCA, k.InsecureSkipVerify)
	if err != nil {
		return err
	}

	if tlsConfig != nil {
		config.Net.TLS.Config = tlsConfig
		config.Net.TLS.Enable = true
	}

	producer, err := sarama.NewSyncProducer(k.Brokers, config)
	if err != nil {
		return err
	}
	k.producer = producer
	return nil
}

func (k *Kafka) Close() error {
	return k.producer.Close()
}

func (k *Kafka) SampleConfig() string {
	return sampleConfig
}

func (k *Kafka) Description() string {
	return "Configuration for the Kafka server to send metrics to"
}

func (k *Kafka) Write(metrics []telegraf.Metric) error {
	if len(metrics) == 0 {
		return nil
	}

	for _, p := range metrics {
		value := p.String()

		m := &sarama.ProducerMessage{
			Topic: k.Topic,
			Value: sarama.StringEncoder(value),
		}
		if h, ok := p.Tags()[k.RoutingTag]; ok {
			m.Key = sarama.StringEncoder(h)
		}

		_, _, err := k.producer.SendMessage(m)
		if err != nil {
			return errors.New(fmt.Sprintf("FAILED to send kafka message: %s\n",
				err))
		}
	}
	return nil
}

func init() {
	outputs.Add("kafka", func() telegraf.Output {
		return &Kafka{}
	})
}
