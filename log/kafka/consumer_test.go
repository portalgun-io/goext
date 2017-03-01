package gxkafka

import (
	"fmt"
	"log"
	"os"
	"os/signal"
	"syscall"
	"testing"
)

import (
	"github.com/Shopify/sarama"
	"strings"
)

func init() {
	sarama.Logger = log.New(os.Stdout, "[Sarama] ", log.LstdFlags)
}

// 默认情况下，Kafka根据传递消息的key来进行分区的分配，即hash(key) % numPartitions,默认情况下，
// Kafka根据传递消息的key来进行分区的分配，即hash(key) % numPartitions,Kafka几乎就是随机找一个分区发送无key的消息，
// 然后把这个分区号加入到缓存中以备后面直接使用
func TestKafkaConsumerWith(t *testing.T) {
	var (
		id       = "consumer-client-id"
		broker   = "127.0.0.1:9092"
		topic    = "test1"
		group    = "go-consumer-group"
		err      error
		brokers  []string
		topics   []string
		consumer Consumer
		cb       ConsumerMessageCallback
	)

	cb = func(message *sarama.ConsumerMessage) error {
		fmt.Printf("receive kafka message %#v\n", message)
		consumer.Commit(message)

		return nil
	}

	brokers = strings.Split(broker, ",")
	topics = strings.Split(topic, ",")
	consumer, err = NewConsumer(id, brokers, topics, group, cb)
	if err != nil {
		t.Fatalf("Failed to initialize Kafka consumer: %v", err)
	}

	err = consumer.Start()
	if err != nil {
		t.Fatalf("Failed to start Kafka consumer: %v", err)
	}

	// signal.Notify的ch信道是阻塞的(signal.Notify不会阻塞发送信号), 需要设置缓冲
	signals := make(chan os.Signal, 1)
	// It is not possible to block SIGKILL or syscall.SIGSTOP
	signal.Notify(signals, os.Interrupt, os.Kill, syscall.SIGHUP, syscall.SIGQUIT, syscall.SIGTERM, syscall.SIGINT)
	for {
		sig := <-signals
		fmt.Printf("got signal %s\n", sig.String())
		switch sig {
		case syscall.SIGHUP:
		// reload()
		default:
			consumer.Stop()
			return
		}
	}
}

func TestKafkaConsumerWithZk(t *testing.T) {
	var (
		id       = "consumer-client-id"
		zk       = "127.0.0.1:2181/kafka"
		topic    = "test1"
		group    = "go-consumer-group-zk"
		err      error
		topics   []string
		consumer Consumer
		cb       ConsumerMessageCallback
	)

	cb = func(message *sarama.ConsumerMessage) error {
		fmt.Printf("receive kafka message %#v\n", message)
		consumer.Commit(message)

		return nil
	}

	topics = strings.Split(topic, ",")
	consumer, err = NewConsumerWithZk(id, zk, topics, group, cb)
	if err != nil {
		t.Fatalf("Failed to initialize Kafka consumer: %v", err)
	}

	err = consumer.Start()
	if err != nil {
		t.Fatalf("Failed to start Kafka consumer: %v", err)
	}

	// signal.Notify的ch信道是阻塞的(signal.Notify不会阻塞发送信号), 需要设置缓冲
	signals := make(chan os.Signal, 1)
	// It is not possible to block SIGKILL or syscall.SIGSTOP
	signal.Notify(signals, os.Interrupt, os.Kill, syscall.SIGHUP, syscall.SIGQUIT, syscall.SIGTERM, syscall.SIGINT)
	for {
		sig := <-signals
		fmt.Printf("got signal %s\n", sig.String())
		switch sig {
		case syscall.SIGHUP:
		// reload()
		default:
			consumer.Stop()
			return
		}
	}
}