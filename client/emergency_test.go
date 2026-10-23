package client

import (
	"bufio"
	"context"
	"fmt"
	"net"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/jvrs2812/go-sip/sip"
)

func TestEmergencyInviteUsesLaboratoryProfile(t *testing.T) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}

	defer listener.Close()

	serverErrors := make(chan error, 1)
	releaseServer := make(chan struct{})
	go func() {
		connection, acceptErr := listener.Accept()
		if acceptErr != nil {
			serverErrors <- acceptErr

			return
		}

		defer connection.Close()

		reader := bufio.NewReader(connection)
		invite, parseErr := sip.ParseReader(reader, sip.DefaultLimits())
		if parseErr != nil {
			serverErrors <- parseErr

			return
		}

		if invite.RequestURI != EmergencyFire || invite.Headers.Get("Priority") != "emergency" {
			serverErrors <- fmt.Errorf("emergency request = %#v", invite)

			return
		}

		if !strings.Contains(invite.Headers.Get("Route"), "127.0.0.1") {
			serverErrors <- fmt.Errorf("emergency Route = %q", invite.Headers.Get("Route"))

			return
		}

		if invite.Headers.Get("Geolocation") != "<https://location.invalid/alice>" || invite.Headers.Get("Geolocation-Routing") != "yes" {
			serverErrors <- fmt.Errorf("geolocation headers = %q, %q", invite.Headers.Get("Geolocation"), invite.Headers.Get("Geolocation-Routing"))

			return
		}

		if writeErr := writeMessage(connection, responseForTest(invite, 486, "Busy Here")); writeErr != nil {
			serverErrors <- writeErr

			return
		}

		ack, parseErr := sip.ParseReader(reader, sip.DefaultLimits())
		if parseErr != nil || ack.Method != "ACK" {
			serverErrors <- fmt.Errorf("emergency ACK = %#v, error = %v", ack, parseErr)

			return
		}

		serverErrors <- nil
		<-releaseServer
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
		TransactionTimeout: time.Second,
	})
	if err != nil {
		t.Fatal(err)
	}

	defer sipClient.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()

	_, err = sipClient.InviteEmergency(ctx, EmergencyProfile{
		ServiceURN:              EmergencyFire,
		LaboratoryRoute:         fmt.Sprintf("sip:lab@127.0.0.1:%d;lr", port),
		GeolocationURI:          "https://location.invalid/alice",
		AllowLocationForRouting: true,
	})
	close(releaseServer)
	serverErr := <-serverErrors

	if err == nil || !strings.Contains(err.Error(), "486") {
		t.Fatalf("InviteEmergency() error = %v, server error = %v", err, serverErr)
	}

	if serverErr != nil {
		t.Fatal(serverErr)
	}
}

func TestEmergencyProfileRejectsPublicRoute(t *testing.T) {
	profile := EmergencyProfile{
		ServiceURN:      EmergencySOS,
		LaboratoryRoute: "sips:emergency.example.com",
	}

	if err := profile.Validate(); err == nil {
		t.Fatal("public emergency route was accepted as a laboratory endpoint")
	}
}
