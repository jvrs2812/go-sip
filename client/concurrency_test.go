package client

import (
	"bufio"
	"context"
	"fmt"
	"net"
	"strconv"
	"sync"
	"testing"
	"time"

	"github.com/jvrs2812/go-sip/sip"
)

func TestConcurrentTransactionsRemainCorrelated(t *testing.T) {
	const transactionCount = 32

	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}

	defer listener.Close()

	serverErrors := make(chan error, 1)
	go func() {
		connection, acceptErr := listener.Accept()
		if acceptErr != nil {
			serverErrors <- acceptErr

			return
		}

		defer connection.Close()

		reader := bufio.NewReader(connection)
		requests := make([]*sip.Message, 0, transactionCount)

		for index := 0; index < transactionCount; index++ {
			request, parseErr := sip.ParseReader(reader, sip.DefaultLimits())
			if parseErr != nil {
				serverErrors <- parseErr

				return
			}

			requests = append(requests, request)
		}

		for index := len(requests) - 1; index >= 0; index-- {
			response := responseForTest(requests[index], 200, "OK")
			response.Headers.Add("X-Request-URI", requests[index].RequestURI)

			if writeErr := writeMessage(connection, response); writeErr != nil {
				serverErrors <- writeErr

				return
			}
		}

		serverErrors <- nil
	}()

	host, portText, err := net.SplitHostPort(listener.Addr().String())
	if err != nil {
		t.Fatal(err)
	}

	port, err := strconv.Atoi(portText)
	if err != nil {
		t.Fatal(err)
	}

	sipClient, err := NewClient(Config{
		ServerHost:         host,
		ServerPort:         port,
		Username:           "1001",
		TransactionTimeout: 3 * time.Second,
	})
	if err != nil {
		t.Fatal(err)
	}

	defer sipClient.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	var waitGroup sync.WaitGroup
	errorsByIndex := make([]error, transactionCount)

	for index := 0; index < transactionCount; index++ {
		index := index
		waitGroup.Add(1)

		go func() {
			defer waitGroup.Done()

			target := fmt.Sprintf("sip:service-%d@example.test", index)
			response, requestErr := sipClient.Options(ctx, target)
			if requestErr != nil {
				errorsByIndex[index] = requestErr

				return
			}

			if response.Headers.Get("X-Request-URI") != target {
				errorsByIndex[index] = fmt.Errorf("response URI = %q, want %q", response.Headers.Get("X-Request-URI"), target)
			}
		}()
	}

	waitGroup.Wait()

	for index, requestErr := range errorsByIndex {
		if requestErr != nil {
			t.Errorf("transaction %d: %v", index, requestErr)
		}
	}

	if err := <-serverErrors; err != nil {
		t.Fatal(err)
	}
}
