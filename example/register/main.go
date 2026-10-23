package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/jvrs2812/go-sip/client"
)

func main() {
	host := flag.String("host", env("SIP_HOST", "127.0.0.1"), "SIP server host")
	port := flag.Int("port", 5060, "SIP server port")
	username := flag.String("username", os.Getenv("SIP_USERNAME"), "SIP username")
	password := flag.String("password", os.Getenv("SIP_PASSWORD"), "SIP password")
	flag.Parse()

	if *username == "" {
		log.Fatal("set -username or SIP_USERNAME")
	}

	sipClient, err := client.NewClient(client.Config{
		ServerHost:         *host,
		ServerPort:         *port,
		Username:           *username,
		Password:           *password,
		TransactionTimeout: 10 * time.Second,
	})

	if err != nil {
		log.Fatal(err)
	}

	defer sipClient.Close()

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	if err := sipClient.Register(ctx); err != nil {
		log.Fatal(err)
	}

	fmt.Println("registered; press Ctrl+C to unregister")
	<-ctx.Done()

	unregisterCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	if err := sipClient.Unregister(unregisterCtx); err != nil {
		log.Printf("unregister: %v", err)
	}
}

func env(name, fallback string) string {
	if value := os.Getenv(name); value != "" {
		return value
	}

	return fallback
}
