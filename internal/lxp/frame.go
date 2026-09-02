// Package lxp implements the LuxPower/EG4 WiFi-dongle TCP framing ("port
// 8000 protocol"). Layout follows the community reverse engineering in
// celsworth/lxp-bridge and jefflaplante/lux PROTOCOL.md; the FlexBoss21 and
// GridBoss speak the same framing with their own register maps.
//
// Wire format (little-endian):
//
//	 0-1  magic 0xA1 0x1A
//	 2-3  protocol version (1 or 2)
//	 4-5  frame length = number of bytes following this field
//	 6    address (1 from client)
//	 7    tcp function: 0xC1 heartbeat, 0xC2 translated data,
//	      0xC3 read param, 0xC4 write param
//	 8-17 datalog (dongle) serial, 10 ASCII bytes
//	next  tcp-function-specific payload
//
// TranslatedData payload:
//
//	 0-1  data length = len(dataFrame)+2 (protocol 2; absent in protocol 1)
//	next  dataFrame + CRC16-Modbus (over dataFrame)
//
// dataFrame:
//
//	 0    address action (0x00 request / 0x01 response bit)
//	 1    device function: 0x03 read hold, 0x04 read input,
//	      0x06 write single, 0x10 write multi
//	 2-11 inverter serial, 10 ASCII bytes
//	12-13 start register
//	14-15 register count (request) — responses carry a value-length byte
//	      followed by register values instead.
package lxp

import (
	"encoding/binary"
	"errors"
	"fmt"
)

const (
	Magic0 = 0xA1
	Magic1 = 0x1A

	FuncHeartbeat      = 0xC1
	FuncTranslatedData = 0xC2
	FuncReadParam      = 0xC3
	FuncWriteParam     = 0xC4

	DevFuncReadHold    = 0x03
	DevFuncReadInput   = 0x04
	DevFuncWriteSingle = 0x06
	DevFuncWriteMulti  = 0x10
)

var funcNames = map[byte]string{
	FuncHeartbeat:      "Heartbeat",
	FuncTranslatedData: "TranslatedData",
	FuncReadParam:      "ReadParam",
	FuncWriteParam:     "WriteParam",
}

var devFuncNames = map[byte]string{
	DevFuncReadHold:    "ReadHold",
	DevFuncReadInput:   "ReadInput",
	DevFuncWriteSingle: "WriteSingle",
	DevFuncWriteMulti:  "WriteMulti",
}

// Frame is a decoded top-level dongle TCP frame.
type Frame struct {
	Protocol     uint16
	Address      byte
	TCPFunction  byte
	DatalogSerial string
	Payload      []byte
}

// TranslatedData is the decoded inner payload of a FuncTranslatedData frame.
type TranslatedData struct {
	AddressAction  byte
	DeviceFunction byte
	InverterSerial string
	Register       uint16
	// Values holds raw register bytes on responses; on requests it is the
	// two-byte register count.
	Values []byte
	CRCOK  bool
}

func (f *Frame) FunctionName() string {
	if n, ok := funcNames[f.TCPFunction]; ok {
		return n
	}
	return fmt.Sprintf("0x%02X", f.TCPFunction)
}

func (td *TranslatedData) DeviceFunctionName() string {
	if n, ok := devFuncNames[td.DeviceFunction]; ok {
		return n
	}
	return fmt.Sprintf("0x%02X", td.DeviceFunction)
}

// BuildReadRequest assembles a protocol-2 TranslatedData read request
// (ReadHold or ReadInput) for count registers starting at reg.
func BuildReadRequest(datalogSerial, inverterSerial string, devFunc byte, reg, count uint16) ([]byte, error) {
	if len(datalogSerial) != 10 || len(inverterSerial) != 10 {
		return nil, errors.New("dongle and inverter serials must each be 10 characters")
	}
	if devFunc != DevFuncReadHold && devFunc != DevFuncReadInput {
		return nil, errors.New("refusing to build non-read request: writes are not supported yet (fail-closed)")
	}

	df := make([]byte, 0, 16)
	df = append(df, 0x00, devFunc)
	df = append(df, inverterSerial...)
	df = binary.LittleEndian.AppendUint16(df, reg)
	df = binary.LittleEndian.AppendUint16(df, count)
	crc := CRC16(df)

	body := make([]byte, 0, 14+len(df)+2)
	body = append(body, 0x01, FuncTranslatedData)
	body = append(body, datalogSerial...)
	body = binary.LittleEndian.AppendUint16(body, uint16(len(df)+2))
	body = append(body, df...)
	body = binary.LittleEndian.AppendUint16(body, crc)

	pkt := make([]byte, 0, 6+len(body))
	pkt = append(pkt, Magic0, Magic1)
	pkt = binary.LittleEndian.AppendUint16(pkt, 2) // protocol
	pkt = binary.LittleEndian.AppendUint16(pkt, uint16(len(body)))
	pkt = append(pkt, body...)
	return pkt, nil
}

// BuildHeartbeatAck echoes a heartbeat frame back to the dongle, which keeps
// long-lived listen connections alive.
func BuildHeartbeatAck(protocol uint16, datalogSerial string) []byte {
	body := make([]byte, 0, 13)
	body = append(body, 0x01, FuncHeartbeat)
	body = append(body, datalogSerial...)
	body = append(body, 0x00)

	pkt := make([]byte, 0, 6+len(body))
	pkt = append(pkt, Magic0, Magic1)
	pkt = binary.LittleEndian.AppendUint16(pkt, protocol)
	pkt = binary.LittleEndian.AppendUint16(pkt, uint16(len(body)))
	pkt = append(pkt, body...)
	return pkt
}

// ParseFrame decodes one frame from buf, returning the frame and the number
// of bytes consumed. Returns (nil, 0, nil) when buf holds an incomplete frame.
func ParseFrame(buf []byte) (*Frame, int, error) {
	if len(buf) < 6 {
		return nil, 0, nil
	}
	if buf[0] != Magic0 || buf[1] != Magic1 {
		return nil, 0, fmt.Errorf("bad magic %02x %02x (not a LuxPower frame)", buf[0], buf[1])
	}
	frameLen := int(binary.LittleEndian.Uint16(buf[4:6]))
	total := 6 + frameLen
	if len(buf) < total {
		return nil, 0, nil
	}
	body := buf[6:total]
	if len(body) < 12 {
		return nil, 0, fmt.Errorf("frame body too short: %d bytes", len(body))
	}
	f := &Frame{
		Protocol:      binary.LittleEndian.Uint16(buf[2:4]),
		Address:       body[0],
		TCPFunction:   body[1],
		DatalogSerial: string(body[2:12]),
		Payload:       body[12:],
	}
	return f, total, nil
}

// ParseTranslatedData decodes the inner payload of a TranslatedData frame.
func (f *Frame) ParseTranslatedData() (*TranslatedData, error) {
	if f.TCPFunction != FuncTranslatedData {
		return nil, fmt.Errorf("frame is %s, not TranslatedData", f.FunctionName())
	}
	p := f.Payload
	if f.Protocol >= 2 {
		if len(p) < 2 {
			return nil, errors.New("short TranslatedData payload")
		}
		p = p[2:] // skip data length
	}
	if len(p) < 16 {
		return nil, fmt.Errorf("TranslatedData too short: %d bytes", len(p))
	}
	df, crcBytes := p[:len(p)-2], p[len(p)-2:]
	td := &TranslatedData{
		AddressAction:  df[0],
		DeviceFunction: df[1],
		InverterSerial: string(df[2:12]),
		Register:       binary.LittleEndian.Uint16(df[12:14]),
		CRCOK:          CRC16(df) == binary.LittleEndian.Uint16(crcBytes),
	}
	if td.DeviceFunction == DevFuncReadHold || td.DeviceFunction == DevFuncReadInput {
		rest := df[14:]
		if td.AddressAction&0x01 != 0 && len(rest) > 1 && int(rest[0]) == len(rest)-1 {
			// response: value-length byte then register bytes
			td.Values = rest[1:]
		} else {
			td.Values = rest
		}
	} else {
		td.Values = df[14:]
	}
	return td, nil
}

// Registers converts response value bytes into u16 register values.
func (td *TranslatedData) Registers() []uint16 {
	out := make([]uint16, 0, len(td.Values)/2)
	for i := 0; i+1 < len(td.Values); i += 2 {
		out = append(out, binary.LittleEndian.Uint16(td.Values[i:i+2]))
	}
	return out
}
