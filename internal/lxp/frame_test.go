package lxp

import (
	"bytes"
	"testing"
)

func TestReadRequestRoundTrip(t *testing.T) {
	pkt, err := BuildReadRequest("BA12345678", "1234567890", DevFuncReadInput, 0, 40)
	if err != nil {
		t.Fatal(err)
	}
	f, n, err := ParseFrame(pkt)
	if err != nil || f == nil {
		t.Fatalf("parse: %v (frame=%v)", err, f)
	}
	if n != len(pkt) {
		t.Fatalf("consumed %d of %d bytes", n, len(pkt))
	}
	if f.TCPFunction != FuncTranslatedData || f.DatalogSerial != "BA12345678" {
		t.Fatalf("bad frame: %+v", f)
	}
	td, err := f.ParseTranslatedData()
	if err != nil {
		t.Fatal(err)
	}
	if !td.CRCOK {
		t.Fatal("crc mismatch on our own frame")
	}
	if td.InverterSerial != "1234567890" || td.Register != 0 || td.DeviceFunction != DevFuncReadInput {
		t.Fatalf("bad translated data: %+v", td)
	}
}

func TestPartialFrameReturnsNil(t *testing.T) {
	pkt, _ := BuildReadRequest("BA12345678", "1234567890", DevFuncReadHold, 12, 1)
	f, n, err := ParseFrame(pkt[:8])
	if f != nil || n != 0 || err != nil {
		t.Fatalf("partial frame should be (nil,0,nil), got (%v,%d,%v)", f, n, err)
	}
}

func TestWriteRequestRefused(t *testing.T) {
	if _, err := BuildReadRequest("BA12345678", "1234567890", DevFuncWriteSingle, 0, 1); err == nil {
		t.Fatal("write device function must be refused (fail-closed)")
	}
}

func TestHeartbeatAckParses(t *testing.T) {
	pkt := BuildHeartbeatAck(2, "BA12345678")
	f, n, err := ParseFrame(pkt)
	if err != nil || f == nil || n != len(pkt) {
		t.Fatalf("parse: %v", err)
	}
	if f.TCPFunction != FuncHeartbeat {
		t.Fatalf("expected heartbeat, got %s", f.FunctionName())
	}
}

func TestCRC16KnownVector(t *testing.T) {
	// Modbus reference vector: 01 04 00 00 00 02 -> CRC bytes 71 CB (0xCB71)
	got := CRC16([]byte{0x01, 0x04, 0x00, 0x00, 0x00, 0x02})
	if got != 0xCB71 {
		t.Fatalf("crc = 0x%04X, want 0xCB71", got)
	}
	if !bytes.Equal([]byte{byte(got), byte(got >> 8)}, []byte{0x71, 0xCB}) {
		t.Fatal("byte order check failed")
	}
}
