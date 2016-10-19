// Copyright 2016 Kochava
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package main

import (
	"log"
	"os"
	"sync"
	"time"

	"github.com/Shopify/sarama"
	metrics "github.com/rcrowley/go-metrics"
)

// GetKafkaConsumer returns a new consumer
func GetKafkaConsumer(custConfig Config, file *os.File) (sarama.Consumer, error) {
	config := sarama.NewConfig()
	config.Consumer.Return.Errors = true

	appMetricRegistry := metrics.NewRegistry()

	consumerMetricRegistry := metrics.NewPrefixedChildRegistry(appMetricRegistry, "consumer.")

	config.MetricRegistry = consumerMetricRegistry

	go metrics.Log(consumerMetricRegistry, 30*time.Second, log.New(file, "consumer: ", log.Lmicroseconds))

	// Create new consumer
	return sarama.NewConsumer(custConfig.srcBrokers, config)

}

// GetKafkaProducer returns a new consumer
func GetKafkaProducer(custConfig Config, file *os.File) (sarama.SyncProducer, error) {
	config := sarama.NewConfig()

	config.Producer.Retry.Max = 5
	config.Producer.RequiredAcks = sarama.WaitForAll
	config.Producer.Partitioner = sarama.NewManualPartitioner
	config.ClientID = "firehose"
	if custConfig.historical {
		config.ClientID = "firehose-historical"
	}

	log.Println("GetKafkaProducer - client id ", config.ClientID)

	appMetricRegistry := metrics.NewRegistry()

	producerMetricRegistry := metrics.NewPrefixedChildRegistry(appMetricRegistry, "producer.")

	config.MetricRegistry = producerMetricRegistry

	go metrics.Log(producerMetricRegistry, 30*time.Second, log.New(file, "producer: ", log.Lmicroseconds))

	// Create new consumer
	return sarama.NewSyncProducer(custConfig.dstBrokers, config)

}

// PullFromTopic pulls messages from the topic partition
func PullFromTopic(consumer sarama.PartitionConsumer,
	producer chan<- sarama.ProducerMessage,
	signals chan os.Signal,
	finalOffset int64,
	syncChan chan int64,
	wg *sync.WaitGroup) {

	defer wg.Done()

	for {
		if len(signals) > 0 {
			log.Println("Consumer - Interrupt is detected - exiting")
			return
		}
		select {
		case err := <-consumer.Errors():
			log.Println(err)
			return
		case consumerMsg := <-consumer.Messages():
			producerMsg := sarama.ProducerMessage{
				Topic:     consumerMsg.Topic,
				Partition: consumerMsg.Partition,
				Key:       sarama.StringEncoder(consumerMsg.Key),
				Value:     sarama.StringEncoder(consumerMsg.Value),
			}
			producer <- producerMsg

			if finalOffset > 0 && consumerMsg.Offset >= finalOffset {
				syncChan <- consumerMsg.Offset
				log.Println("Consumer - partition ", consumerMsg.Partition, " reached final offset, shutting down partition")
				return
			}
		}
	}
}

// PushToTopic pushes messages to topic
func PushToTopic(producer sarama.SyncProducer,
	consumer <-chan sarama.ProducerMessage,
	signals chan os.Signal,
	syncChan chan int64,
	wg *sync.WaitGroup) {

	defer wg.Done()

	for {
		if len(signals) > 0 {
			log.Println("Producer - Interrupt is detected - exiting")
			return
		}
		select {
		case consumerMsg := <-consumer:
			_, _, err := producer.SendMessage(&consumerMsg)
			if err != nil {
				log.Println("Failed to produce message to kafka cluster. ", err)
				return
			}
		}
		if len(consumer) <= 0 && len(syncChan) > 0 {
			parNum := <-syncChan
			log.Println("Producer - partition ", parNum, " finished, closing partition")
			return
		}
	}
}

// MonitorChan monitors the transfer channel
func MonitorChan(transferChan chan sarama.ProducerMessage, signals chan os.Signal, wg *sync.WaitGroup) {
	defer wg.Done()
	for {
		if len(signals) > 0 {
			log.Println("Monitor - Interrupt is detected - exiting")
			return
		}
		log.Println("Transfer channel length: ", len(transferChan))
		time.Sleep(10 * time.Second)
	}
}

// CloseProducer Closes the producer
func CloseProducer(producer *sarama.SyncProducer) {
	log.Println("Closing producer client")
	if err := (*producer).Close(); err != nil {
		// Should not reach here
		log.Println(err)
	}
}

// CloseConsumer closes the consumer
func CloseConsumer(consumer *sarama.Consumer) {
	log.Println("Closing consumer client")
	if err := (*consumer).Close(); err != nil {
		// Should not reach here
		log.Println(err)
	}
}
