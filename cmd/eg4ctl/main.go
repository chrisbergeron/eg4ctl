// eg4ctl — CLI for EG4 FlexBoss21 / GridBoss inverters over the LAN dongle
// protocol (LuxPower TCP port 8000). Read-only by design in v0.
package main

import (
	"context"
	"encoding/hex"
	"encoding/json"
	"flag"
	"fmt"
	"net"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/chrisbergeron/eg4ctl/internal/dongle"
	"github.com/chrisbergeron/eg4ctl/internal/lxp"
)

var version = "0.1.0-dev"

func usage() {
	fmt.Fprintf(os.Stderr, `eg4ctl %s — EG4 FlexBoss21/GridBoss CLI (read-only)

Usage: eg4ctl <command> [flags]

Commands:
  discover   Scan a subnet for dongle candidates (TCP %s open)
  listen     Connect to a dongle and print every frame it pushes
  read       Read input/hold registers (needs --dongle-serial and --inverter-serial)
  status     Read input bank 0 and print decoded fields (EXPERIMENTAL) + raw
  version    Print version

Environment defaults: EG4_HOST, EG4_DONGLE_SERIAL, EG4_INVERTER_SERIAL
`, version, "8000")
	os.Exit(2)
}

func main() {
	if len(os.Args) < 2 {
		usage()
	}
	cmd, args := os.Args[1], os.Args[2:]
	var err error
	switch cmd {
	case "discover":
		err = cmdDiscover(args)
	case "listen":
		err = cmdListen(args)
	case "read":
		err = cmdRead(args)
	case "status":
		err = cmdStatus(args)
	case "version":
		fmt.Println(version)
	default:
		usage()
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, "eg4ctl:", err)
		os.Exit(1)
	}
}

func envDefault(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}

func cmdDiscover(args []string) error {
	fs := flag.NewFlagSet("discover", flag.ExitOnError)
	cidr := fs.String("cidr", "192.168.42.0/24", "subnet to scan")
	port := fs.Int("port", 8000, "TCP port")
	timeout := fs.Duration("timeout", 1500*time.Millisecond, "per-host connect timeout")
	fs.Parse(args)

	prefix, err := parseCIDR(*cidr)
	if err != nil {
		return err
	}
	var (
		wg   sync.WaitGroup
		mu   sync.Mutex
		open []string
		sem  = make(chan struct{}, 64)
	)
	for _, ip := range prefix {
		wg.Add(1)
		sem <- struct{}{}
		go func(ip string) {
			defer wg.Done()
			defer func() { <-sem }()
			addr := net.JoinHostPort(ip, fmt.Sprint(*port))
			c, err := net.DialTimeout("tcp", addr, *timeout)
			if err == nil {
				c.Close()
				mu.Lock()
				open = append(open, ip)
				mu.Unlock()
			}
		}(ip)
	}
	wg.Wait()
	if len(open) == 0 {
		fmt.Printf("no hosts with tcp/%d open in %s\n", *port, *cidr)
		return nil
	}
	fmt.Printf("tcp/%d open on:\n", *port)
	for _, ip := range open {
		fmt.Println(" ", ip)
	}
	fmt.Println("verify a candidate with: eg4ctl listen --host <ip> --duration 150s")
	return nil
}

func parseCIDR(cidr string) ([]string, error) {
	_, ipnet, err := net.ParseCIDR(cidr)
	if err != nil {
		return nil, err
	}
	var ips []string
	for ip := ipnet.IP.Mask(ipnet.Mask); ipnet.Contains(ip); ip = nextIP(ip) {
		ips = append(ips, ip.String())
	}
	if len(ips) > 2 {
		ips = ips[1 : len(ips)-1] // drop network + broadcast
	}
	if len(ips) > 4096 {
		return nil, fmt.Errorf("refusing to scan %d hosts; narrow the CIDR", len(ips))
	}
	return ips, nil
}

func nextIP(ip net.IP) net.IP {
	out := make(net.IP, len(ip))
	copy(out, ip)
	for i := len(out) - 1; i >= 0; i-- {
		out[i]++
		if out[i] != 0 {
			break
		}
	}
	return out
}

func dial(fs *flag.FlagSet, args []string) (*dongle.Client, *flag.FlagSet, error) {
	host := fs.String("host", envDefault("EG4_HOST", ""), "dongle IP or host")
	port := fs.Int("port", 8000, "dongle TCP port")
	ds := fs.String("dongle-serial", os.Getenv("EG4_DONGLE_SERIAL"), "10-char dongle (datalog) serial")
	is := fs.String("inverter-serial", os.Getenv("EG4_INVERTER_SERIAL"), "10-char inverter serial")
	fs.Parse(args)
	if *host == "" {
		return nil, fs, fmt.Errorf("--host (or EG4_HOST) is required")
	}
	c := &dongle.Client{
		Addr:           net.JoinHostPort(*host, fmt.Sprint(*port)),
		DatalogSerial:  *ds,
		InverterSerial: *is,
	}
	if err := c.Connect(context.Background()); err != nil {
		return nil, fs, err
	}
	return c, fs, nil
}

func cmdListen(args []string) error {
	fs := flag.NewFlagSet("listen", flag.ExitOnError)
	duration := fs.Duration("duration", 150*time.Second, "how long to listen")
	c, _, err := dial(fs, args)
	if err != nil {
		return err
	}
	defer c.Close()
	fmt.Fprintf(os.Stderr, "connected to %s; listening %s (dongle pushes ~every 2 min)\n", c.Addr, *duration)
	end := time.Now().Add(*duration)
	frames := 0
	for time.Now().Before(end) {
		f, err := c.NextFrame(end)
		if err != nil {
			if ne, ok := err.(net.Error); ok && ne.Timeout() {
				break
			}
			return err
		}
		frames++
		fmt.Printf("[%s] proto=%d func=%s dongle=%s payload=%dB\n",
			time.Now().Format("15:04:05"), f.Protocol, f.FunctionName(), f.DatalogSerial, len(f.Payload))
		if td, err := f.ParseTranslatedData(); err == nil {
			fmt.Printf("  %s inverter=%s reg=%d crc_ok=%v values=%dB\n",
				td.DeviceFunctionName(), td.InverterSerial, td.Register, td.CRCOK, len(td.Values))
			fmt.Printf("  %s\n", hex.EncodeToString(td.Values))
		} else if len(f.Payload) > 0 {
			fmt.Printf("  raw: %s\n", hex.EncodeToString(f.Payload))
		}
	}
	if frames == 0 {
		fmt.Println("no frames received — wrong host, dongle firmware with port 8000 disabled, or inverter asleep")
	}
	return nil
}

func requireSerials(c *dongle.Client) error {
	if len(c.DatalogSerial) != 10 || len(c.InverterSerial) != 10 {
		return fmt.Errorf("reads need --dongle-serial and --inverter-serial (10 chars each; see dongle sticker or EG4 Monitor app)")
	}
	return nil
}

func cmdRead(args []string) error {
	fs := flag.NewFlagSet("read", flag.ExitOnError)
	bank := fs.String("type", "input", "register type: input|hold")
	reg := fs.Uint("reg", 0, "start register")
	count := fs.Uint("count", 40, "register count")
	wait := fs.Duration("wait", 15*time.Second, "response timeout")
	c, _, err := dial(fs, args)
	if err != nil {
		return err
	}
	defer c.Close()
	if err := requireSerials(c); err != nil {
		return err
	}
	devFunc := byte(lxp.DevFuncReadInput)
	if strings.HasPrefix(*bank, "h") {
		devFunc = lxp.DevFuncReadHold
	}
	td, err := c.ReadRegisters(devFunc, uint16(*reg), uint16(*count), *wait)
	if err != nil {
		return err
	}
	regs := td.Registers()
	for i, v := range regs {
		fmt.Printf("%s[%d] = %d (0x%04x)\n", *bank, int(*reg)+i, v, v)
	}
	return nil
}

func cmdStatus(args []string) error {
	fs := flag.NewFlagSet("status", flag.ExitOnError)
	format := fs.String("format", "text", "output: text|json")
	wait := fs.Duration("wait", 15*time.Second, "response timeout")
	c, _, err := dial(fs, args)
	if err != nil {
		return err
	}
	defer c.Close()
	if err := requireSerials(c); err != nil {
		return err
	}
	td, err := c.ReadRegisters(lxp.DevFuncReadInput, 0, 40, *wait)
	if err != nil {
		return err
	}
	b0, err := lxp.DecodeInputBank0(td.Values)
	if err != nil {
		return err
	}
	if *format == "json" {
		out := map[string]any{
			"experimental": true,
			"inverter":     td.InverterSerial,
			"decoded":      b0,
			"raw_hex":      hex.EncodeToString(td.Values),
			"ts":           time.Now().Format(time.RFC3339),
		}
		return json.NewEncoder(os.Stdout).Encode(out)
	}
	fmt.Printf("inverter %s (EXPERIMENTAL decode — verify offsets, CB-2)\n", td.InverterSerial)
	fmt.Printf("  status      %d\n", b0.Status)
	fmt.Printf("  soc         %d%%   soh %d%%\n", b0.SOC, b0.SOH)
	fmt.Printf("  battery     %.1f V  charge %d W  discharge %d W\n", b0.VBat, b0.PCharge, b0.PDischarge)
	fmt.Printf("  pv          %.1f/%.1f/%.1f V  %d/%d/%d W\n", b0.VPV1, b0.VPV2, b0.VPV3, b0.PPV1, b0.PPV2, b0.PPV3)
	fmt.Printf("  raw         %s\n", hex.EncodeToString(td.Values))
	return nil
}
