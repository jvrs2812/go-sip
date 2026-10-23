package client

import (
	"context"
	"errors"
	"fmt"
	"net"
	"strings"

	"github.com/jvrs2812/go-sip/sip"
)

const (
	EmergencySOS       = "urn:service:sos"
	EmergencyAmbulance = "urn:service:sos.ambulance"
	EmergencyFire      = "urn:service:sos.fire"
	EmergencyPolice    = "urn:service:sos.police"
)

type EmergencyProfile struct {
	ServiceURN              string
	LaboratoryRoute         string
	GeolocationURI          string
	AllowLocationForRouting bool
}

func (c *Client) InviteEmergency(ctx context.Context, profile EmergencyProfile) (*Call, error) {
	if strings.TrimSpace(profile.ServiceURN) == "" {
		profile.ServiceURN = EmergencySOS
	}

	if err := profile.Validate(); err != nil {
		return nil, err
	}

	return c.invite(ctx, profile.ServiceURN, func(request *sip.Message) error {
		request.Headers.Add("Route", nameAddress(profile.LaboratoryRoute))
		request.Headers.Add("Priority", "emergency")

		if profile.GeolocationURI != "" {
			request.Headers.Add("Geolocation", nameAddress(profile.GeolocationURI))

			permission := "no"
			if profile.AllowLocationForRouting {
				permission = "yes"
			}

			request.Headers.Add("Geolocation-Routing", permission)
		}

		return nil
	})
}

func (p EmergencyProfile) Validate() error {
	service := strings.ToLower(strings.TrimSpace(p.ServiceURN))
	if service == "" {
		service = EmergencySOS
	}

	if service != EmergencySOS && !strings.HasPrefix(service, EmergencySOS+".") {
		return errors.New("emergency service must be an urn:service:sos URI")
	}

	if !isLaboratoryRoute(p.LaboratoryRoute) {
		return errors.New("emergency route must use a loopback, private, .test, or .invalid laboratory endpoint")
	}

	if p.GeolocationURI != "" {
		location := strings.Trim(strings.TrimSpace(p.GeolocationURI), "<>")
		if !strings.HasPrefix(strings.ToLower(location), "cid:") && !strings.HasPrefix(strings.ToLower(location), "https:") {
			return errors.New("emergency geolocation must use cid: or https:")
		}
	}

	return nil
}

func isLaboratoryRoute(value string) bool {
	route := strings.Trim(strings.TrimSpace(value), "<>")
	if route == "" {
		return false
	}

	parsed, err := sip.ParseURI(route)
	if err != nil {
		return false
	}

	host := strings.ToLower(strings.Trim(parsed.Host, "[]"))
	if host == "localhost" || strings.HasSuffix(host, ".test") || strings.HasSuffix(host, ".invalid") {
		return true
	}

	ip := net.ParseIP(host)

	return ip != nil && (ip.IsLoopback() || ip.IsPrivate())
}

func nameAddress(value string) string {
	value = strings.TrimSpace(value)
	if strings.HasPrefix(value, "<") && strings.HasSuffix(value, ">") {
		return value
	}

	return fmt.Sprintf("<%s>", value)
}
