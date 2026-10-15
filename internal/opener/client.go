package opener

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"time"
)

const GuestSocket = "/mnt/workshop-host/open/socket"

// Open sends one request, including connection and response time in the deadline.
func Open(ctx context.Context, target string) error {
	uri, err := GuestURI(target)
	if err != nil {
		return err
	}

	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()

	var dialer net.Dialer
	conn, err := dialer.DialContext(ctx, "unix", GuestSocket)
	if err != nil {
		return err
	}
	defer conn.Close()

	stop := context.AfterFunc(ctx, func() { conn.Close() })
	defer stop()

	deadline, _ := ctx.Deadline()
	if err := conn.SetDeadline(deadline); err != nil {
		return err
	}

	return exchange(conn, uri)
}

func exchange(stream io.ReadWriter, uri string) error {
	message, err := json.Marshal(Request{URI: uri})
	if err != nil {
		return err
	}

	message = append(message, '\n')
	if len(message) > MaxRequest {
		return fmt.Errorf("open request is too large")
	}

	n, err := stream.Write(message)
	if err != nil {
		return err
	}
	if n != len(message) {
		return io.ErrShortWrite
	}

	line, err := bufio.NewReader(io.LimitReader(stream, MaxRequest+1)).ReadBytes('\n')
	if err != nil || len(line) > MaxRequest {
		return fmt.Errorf("invalid response from the host desktop bridge")
	}

	var response Response
	if err := json.Unmarshal(line, &response); err != nil {
		return fmt.Errorf("invalid host response: %w", err)
	}
	if response.OK {
		return nil
	}
	if response.Error != "" {
		return fmt.Errorf("host open request failed: %s", response.Error)
	}
	return fmt.Errorf("host open request failed")
}
