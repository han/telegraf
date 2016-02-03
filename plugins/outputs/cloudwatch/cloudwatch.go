package cloudwatch

import (
	"log"
	"math"
	"sort"
	"strings"
	"time"

	"github.com/aws/aws-sdk-go/aws"
	"github.com/aws/aws-sdk-go/aws/credentials"
	"github.com/aws/aws-sdk-go/aws/credentials/ec2rolecreds"
	"github.com/aws/aws-sdk-go/aws/ec2metadata"
	"github.com/aws/aws-sdk-go/aws/session"
	"github.com/aws/aws-sdk-go/service/cloudwatch"

	"github.com/influxdata/telegraf"
	"github.com/influxdata/telegraf/plugins/outputs"
)

type CloudWatch struct {
	Region    string // AWS Region
	Namespace string // CloudWatch Metrics Namespace
	svc       *cloudwatch.CloudWatch
}

var sampleConfig = `
  ### Amazon REGION
  region = 'us-east-1'

  ### Namespace for the CloudWatch MetricDatums
  namespace = 'InfluxData/Telegraf'
`

func (c *CloudWatch) SampleConfig() string {
	return sampleConfig
}

func (c *CloudWatch) Description() string {
	return "Configuration for AWS CloudWatch output."
}

func (c *CloudWatch) Connect() error {
	Config := &aws.Config{
		Region: aws.String(c.Region),
		Credentials: credentials.NewChainCredentials(
			[]credentials.Provider{
				&ec2rolecreds.EC2RoleProvider{Client: ec2metadata.New(session.New())},
				&credentials.EnvProvider{},
				&credentials.SharedCredentialsProvider{},
			}),
	}

	svc := cloudwatch.New(session.New(Config))

	params := &cloudwatch.ListMetricsInput{
		Namespace: aws.String(c.Namespace),
	}

	_, err := svc.ListMetrics(params) // Try a read-only call to test connection.

	if err != nil {
		log.Printf("cloudwatch: Error in ListMetrics API call : %+v \n", err.Error())
	}

	c.svc = svc

	return err
}

func (c *CloudWatch) Close() error {
	return nil
}

func (c *CloudWatch) Write(metrics []telegraf.Metric) error {
	for _, m := range metrics {
		err := c.WriteSinglePoint(m)
		if err != nil {
			return err
		}
	}

	return nil
}

// Write data for a single point. A point can have many fields and one field
// is equal to one MetricDatum. There is a limit on how many MetricDatums a
// request can have so we process one Point at a time.
func (c *CloudWatch) WriteSinglePoint(point telegraf.Metric) error {
	datums := BuildMetricDatum(point)

	const maxDatumsPerCall = 20 // PutMetricData only supports up to 20 data metrics per call

	for _, partition := range PartitionDatums(maxDatumsPerCall, datums) {
		err := c.WriteToCloudWatch(partition)

		if err != nil {
			return err
		}
	}

	return nil
}

func (c *CloudWatch) WriteToCloudWatch(datums []*cloudwatch.MetricDatum) error {
	params := &cloudwatch.PutMetricDataInput{
		MetricData: datums,
		Namespace:  aws.String(c.Namespace),
	}

	_, err := c.svc.PutMetricData(params)

	if err != nil {
		log.Printf("CloudWatch: Unable to write to CloudWatch : %+v \n", err.Error())
	}

	return err
}

// Partition the MetricDatums into smaller slices of a max size so that are under the limit
// for the AWS API calls.
func PartitionDatums(size int, datums []*cloudwatch.MetricDatum) [][]*cloudwatch.MetricDatum {

	numberOfPartitions := len(datums) / size
	if len(datums)%size != 0 {
		numberOfPartitions += 1
	}

	partitions := make([][]*cloudwatch.MetricDatum, numberOfPartitions)

	for i := 0; i < numberOfPartitions; i++ {
		start := size * i
		end := size * (i + 1)
		if end > len(datums) {
			end = len(datums)
		}

		partitions[i] = datums[start:end]
	}

	return partitions
}

// Make a MetricDatum for each field in a Point. Only fields with values that can be
// converted to float64 are supported. Non-supported fields are skipped.
func BuildMetricDatum(point telegraf.Metric) []*cloudwatch.MetricDatum {
	datums := make([]*cloudwatch.MetricDatum, len(point.Fields()))
	i := 0

	var value float64

	for k, v := range point.Fields() {
		switch t := v.(type) {
		case int:
			value = float64(t)
		case int32:
			value = float64(t)
		case int64:
			value = float64(t)
		case float64:
			value = t
		case bool:
			if t {
				value = 1
			} else {
				value = 0
			}
		case time.Time:
			value = float64(t.Unix())
		default:
			// Skip unsupported type.
			datums = datums[:len(datums)-1]
			continue
		}

		datums[i] = &cloudwatch.MetricDatum{
			MetricName: aws.String(strings.Join([]string{point.Name(), k}, "_")),
			Value:      aws.Float64(value),
			Dimensions: BuildDimensions(point.Tags()),
			Timestamp:  aws.Time(point.Time()),
		}

		i += 1
	}

	return datums
}

// Make a list of Dimensions by using a Point's tags. CloudWatch supports up to
// 10 dimensions per metric so we only keep up to the first 10 alphabetically.
// This always includes the "host" tag if it exists.
func BuildDimensions(mTags map[string]string) []*cloudwatch.Dimension {

	const MaxDimensions = 10
	dimensions := make([]*cloudwatch.Dimension, int(math.Min(float64(len(mTags)), MaxDimensions)))

	i := 0

	// This is pretty ugly but we always want to include the "host" tag if it exists.
	if host, ok := mTags["host"]; ok {
		dimensions[i] = &cloudwatch.Dimension{
			Name:  aws.String("host"),
			Value: aws.String(host),
		}
		i += 1
	}

	var keys []string
	for k := range mTags {
		if k != "host" {
			keys = append(keys, k)
		}
	}
	sort.Strings(keys)

	for _, k := range keys {
		if i >= MaxDimensions {
			break
		}

		dimensions[i] = &cloudwatch.Dimension{
			Name:  aws.String(k),
			Value: aws.String(mTags[k]),
		}

		i += 1
	}

	return dimensions
}

func init() {
	outputs.Add("cloudwatch", func() telegraf.Output {
		return &CloudWatch{}
	})
}
