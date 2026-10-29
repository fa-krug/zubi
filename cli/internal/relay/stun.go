package relay

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/binary"
	"errors"
	"fmt"
	"net"
	"time"
)

// STUNServers answer the relay's one question, which address its network has on the internet
// (RFC 8489 Binding). Two operators, so one being down isn't fatal.
var STUNServers = []string{"stun.cloudflare.com:3478", "stun.l.google.com:19302"}

const stunMagicCookie = 0x2112A442

// PublicIPv4 asks the servers in turn for this network's public IPv4 address.
func PublicIPv4(ctx context.Context, servers []string) (net.IP, error) {
	var errs []error
	for _, server := range servers {
		for range 2 {
			ip, err := stunBinding(ctx, server, 2*time.Second)
			if err == nil {
				return ip, nil
			}
			errs = append(errs, fmt.Errorf("%s: %w", server, err))
			if ctx.Err() != nil {
				return nil, errors.Join(errs...)
			}
		}
	}
	return nil, errors.Join(errs...)
}

func stunBinding(ctx context.Context, server string, timeout time.Duration) (net.IP, error) {
	var d net.Dialer
	conn, err := d.DialContext(ctx, "udp4", server)
	if err != nil {
		return nil, err
	}
	defer conn.Close()
	deadline := time.Now().Add(timeout)
	if d, ok := ctx.Deadline(); ok && d.Before(deadline) {
		deadline = d
	}
	conn.SetDeadline(deadline)

	request := make([]byte, 20)
	binary.BigEndian.PutUint16(request[0:], 0x0001) // Binding request, no attributes
	binary.BigEndian.PutUint32(request[4:], stunMagicCookie)
	txID := request[8:20]
	_, _ = rand.Read(txID)
	if _, err := conn.Write(request); err != nil {
		return nil, err
	}
	buf := make([]byte, 1500)
	for {
		n, err := conn.Read(buf)
		if err != nil {
			return nil, err
		}
		if ip, err := parseBindingResponse(buf[:n], txID); err == nil {
			return ip, nil
		} else if !errors.Is(err, errNotOurs) {
			return nil, err
		}
	}
}

var errNotOurs = errors.New("not an answer to this request")

// parseBindingResponse reads the mapped address from a Binding success response to txID.
func parseBindingResponse(msg, txID []byte) (net.IP, error) {
	if len(msg) < 20 || binary.BigEndian.Uint32(msg[4:]) != stunMagicCookie || !bytes.Equal(msg[8:20], txID) {
		return nil, errNotOurs
	}
	if binary.BigEndian.Uint16(msg[0:]) != 0x0101 {
		return nil, errors.New("the STUN server refused the request")
	}
	length := int(binary.BigEndian.Uint16(msg[2:]))
	if 20+length > len(msg) {
		return nil, errors.New("truncated STUN response")
	}
	attrs := msg[20 : 20+length]
	var mapped net.IP
	for len(attrs) >= 4 {
		typ := binary.BigEndian.Uint16(attrs[0:])
		size := int(binary.BigEndian.Uint16(attrs[2:]))
		if 4+size > len(attrs) {
			break
		}
		value := attrs[4 : 4+size]
		switch typ {
		case 0x0020: // XOR-MAPPED-ADDRESS
			if len(value) == 8 && value[1] == 0x01 {
				ip := make(net.IP, 4)
				binary.BigEndian.PutUint32(ip, binary.BigEndian.Uint32(value[4:])^stunMagicCookie)
				return ip, nil
			}
		case 0x0001: // MAPPED-ADDRESS, from servers that predate RFC 5389
			if len(value) == 8 && value[1] == 0x01 {
				mapped = net.IP(bytes.Clone(value[4:8]))
			}
		}
		attrs = attrs[min(len(attrs), 4+(size+3)&^3):] // values are padded to four bytes
	}
	if mapped != nil {
		return mapped, nil
	}
	return nil, errors.New("the STUN response has no IPv4 address")
}
