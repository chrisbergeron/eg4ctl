// Package dongle is a TCP client for the EG4/LuxPower WiFi-Ethernet dongle's
// LAN interface on port 8000. Read-only: it can listen for frames the dongle
// pushes (input register banks roughly every 2 minutes) and issue read
// requests. Write functions are intentionally not implemented yet.
package dongle

import (
	"context"
	"fmt"
	"net"
	"time"

	"github.com/chrisbergeron/eg4ctl/internal/lxp"
)

type Client struct {
	Addr           string // host:port
	DatalogSerial  string
	InverterSerial string
	Timeout        time.Duration

	conn net.Conn
	buf  []byte
}

func (c *Client) Connect(ctx context.Context) error {
	if c.Timeout == 0 {
		c.Timeout = 10 * time.Second
	}
	d := net.Dialer{Timeout: c.Timeout}
	conn, err := d.DialContext(ctx, "tcp", c.Addr)
	if err != nil {
		return fmt.Errorf("dial %s: %w", c.Addr, err)
	}
	c.conn = conn
	return nil
}

func (c *Client) Close() error {
	if c.conn != nil {
		return c.conn.Close()
	}
	return nil
}

// NextFrame reads one frame, answering heartbeats automatically so the
// dongle keeps the session alive. Returns net timeout errors unwrapped so
// callers can treat them as "no data yet".
func (c *Client) NextFrame(deadline time.Time) (*lxp.Frame, error) {
	for {
		if f, n, err := lxp.ParseFrame(c.buf); err != nil {
			c.buf = nil // desync: drop buffer, resync on next magic
			return nil, err
		} else if f != nil {
			c.buf = c.buf[n:]
			if f.TCPFunction == lxp.FuncHeartbeat {
				_, _ = c.conn.Write(lxp.BuildHeartbeatAck(f.Protocol, f.DatalogSerial))
				continue
			}
			return f, nil
		}
		if err := c.conn.SetReadDeadline(deadline); err != nil {
			return nil, err
		}
		chunk := make([]byte, 4096)
		n, err := c.conn.Read(chunk)
		if n > 0 {
			c.buf = append(c.buf, chunk[:n]...)
		}
		if err != nil {
			return nil, err
		}
	}
}

// ReadRegisters sends a read request (ReadInput or ReadHold) and waits for
// the matching TranslatedData response.
func (c *Client) ReadRegisters(devFunc byte, reg, count uint16, wait time.Duration) (*lxp.TranslatedData, error) {
	pkt, err := lxp.BuildReadRequest(c.DatalogSerial, c.InverterSerial, devFunc, reg, count)
	if err != nil {
		return nil, err
	}
	if _, err := c.conn.Write(pkt); err != nil {
		return nil, fmt.Errorf("write request: %w", err)
	}
	deadline := time.Now().Add(wait)
	for time.Now().Before(deadline) {
		f, err := c.NextFrame(deadline)
		if err != nil {
			return nil, fmt.Errorf("awaiting response: %w", err)
		}
		if f.TCPFunction != lxp.FuncTranslatedData {
			continue
		}
		td, err := f.ParseTranslatedData()
		if err != nil {
			continue
		}
		if td.DeviceFunction == devFunc && td.Register == reg {
			if !td.CRCOK {
				return nil, fmt.Errorf("response CRC mismatch (reg %d)", reg)
			}
			return td, nil
		}
	}
	return nil, fmt.Errorf("no response for reg %d within %s", reg, wait)
}
