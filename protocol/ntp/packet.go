package ntp

import (
	"bytes"
	"encoding/binary"
	"net"
	"time"
	"unsafe"

	syscall "golang.org/x/sys/unix"
)

// NTPPacketSizeBytes sets the size of NTP packet
const NTPPacketSizeBytes = 48

// ControlHeaderSizeBytes is a buffer to read packet header with Kernel/HW timestamps
const ControlHeaderSizeBytes = 32

// Packet is an NTP packet
/*
	http://seriot.ch/ntp.php
	https://tools.ietf.org/html/rfc958
	0                   1                   2                   3
	0 1 2 3 4 5 6 7 8 9 0 1 2 3 4 5 6 7 8 9 0 1 2 3 4 5 6 7 8 9 0 1
0 +-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+
	|LI | VN  |Mode |    Stratum     |     Poll      |  Precision   |
4 +-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+
	|                         Root Delay                            |
8 +-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+
	|                         Root Dispersion                       |
12+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+
	|                          Reference ID                         |
16+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+
	|                                                               |
	+                     Reference Timestamp (64)                  +
	|                                                               |
24+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+
	|                                                               |
	+                      Origin Timestamp (64)                    +
	|                                                               |
32+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+
	|                                                               |
	+                      Receive Timestamp (64)                   +
	|                                                               |
40+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+
	|                                                               |
	+                      Transmit Timestamp (64)                  +
	|                                                               |
48+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+
*/
/*
	Setting = LI | VN  |Mode. Client request example:
	00 011 011 (or 0x1B)
	|  |   +-- client mode (3)
	|  + ----- version (3)
	+ -------- leap year indicator, 0 no warning
*/
type Packet struct {
	Settings       uint8  // leap yr indicator, ver number, and mode
	Stratum        uint8  // stratum of local clock
	Poll           int8   // poll exponent
	Precision      int8   // precision exponent
	RootDelay      uint32 // root delay
	RootDispersion uint32 // root dispersion
	ReferenceID    uint32 // reference id
	RefTimeSec     uint32 // reference timestamp sec
	RefTimeFrac    uint32 // reference timestamp nanosec
	OrigTimeSec    uint32 // origin time secs
	OrigTimeFrac   uint32 // origin time nanosec
	RxTimeSec      uint32 // receive time secs
	RxTimeFrac     uint32 // receive time nanosec
	TxTimeSec      uint32 // transmit time secs
	TxTimeFrac     uint32 // transmit time nanosec
}

const (
	liNoWarning      = 0
	liAlarmCondition = 3
	vnFirst          = 1
	vnLast           = 4
	modeClient       = 3
)

// ValidSettingsFormat verifies that LI | VN  |Mode fields are set correctly
// check the first byte,include:
// 	LN:must be 0 or 3
// 	VN:must be 1,2,3 or 4
//	Mode:must be 3
func (p *Packet) ValidSettingsFormat() bool {
	settings := p.Settings
	var l = settings >> 6
	var v = (settings << 2) >> 5
	var m = (settings << 5) >> 5
	if (l == liNoWarning) || (l == liAlarmCondition) {
		if (v >= vnFirst) && (v <= vnLast) {
			if m == modeClient {
				return true
			}
		}
	}
	return false
}

// Bytes converts Packet to []bytes
func (p *Packet) Bytes() ([]byte, error) {
	var bytes bytes.Buffer
	err := binary.Write(&bytes, binary.BigEndian, p)
	return bytes.Bytes(), err
}

// BytesToPacket converts []bytes to Packet
func BytesToPacket(ntpPacketBytes []byte) (*Packet, error) {
	packet := &Packet{}
	reader := bytes.NewReader(ntpPacketBytes)
	err := binary.Read(reader, binary.BigEndian, packet)
	return packet, err
}

// ReadNTPPacket reads incoming NTP packet
func ReadNTPPacket(conn *net.UDPConn) (ntp *Packet, remAddr net.Addr, err error) {
	buf := make([]byte, NTPPacketSizeBytes)
	_, remAddr, err = conn.ReadFromUDP(buf)
	if err != nil {
		return nil, nil, err
	}
	ntp, err = BytesToPacket(buf)

	return ntp, remAddr, err
}

// ReadPacketWithKernelTimestamp reads HW/kernel timestamp from incoming packet
func ReadPacketWithKernelTimestamp(conn *net.UDPConn) (ntp *Packet, hwRxTime time.Time, remAddr net.Addr, err error) {
	// Get socket fd
	connfd, err := connFd(conn)
	if err != nil {
		return nil, time.Time{}, nil, err
	}
	buf := make([]byte, NTPPacketSizeBytes)
	oob := make([]byte, ControlHeaderSizeBytes)

	// Receive message + control struct from the socket
	// https://linux.die.net/man/2/recvmsg
	// This is a low-level way of getting the message (NTP packet content)
	// Additionally we receive control headers, one of which is hwtimestamp
	_, _, _, sa, err := syscall.Recvmsg(connfd, buf, oob, 0)
	if err != nil {
		return nil, time.Time{}, nil, err
	}
	// Extract hardware timestamp from control fields
	ts := (*syscall.Timespec)(unsafe.Pointer(&oob[syscall.CmsgSpace(0)]))
	hwRxTime = time.Unix(ts.Unix())

	packet, err := BytesToPacket(buf)
	remAddr = sockaddrToUDP(sa)
	return packet, hwRxTime, remAddr, err
}