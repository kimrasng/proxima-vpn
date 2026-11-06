package connectionlog

import (
	"encoding/binary"
	"errors"
	"net/netip"
)

var (
	errWGPacket            = errors.New("malformed inner IP packet")
	errWGUnsupportedPacket = errors.New("unsupported inner IP packet")
)

// No payload survives parsing. Offsets describe fragment coverage, not bytes.
type wgPacket struct {
	source, destination            netip.Addr
	sourcePort, destinationPort    uint16
	protocol                       uint8
	length                         uint64
	flags                          uint8
	sequence                       uint32
	fragmented                     bool
	fragmentID                     uint32
	fragmentProtocol               uint8
	fragmentOffset, fragmentLength uint32
	more                           bool
}

func parseWGPacket(data []byte) (wgPacket, error) {
	p := wgPacket{}
	if len(data) == 0 {
		return p, errWGPacket
	}
	var offset int
	switch data[0] >> 4 {
	case 4:
		if len(data) < 20 {
			return p, errWGPacket
		}
		offset = int(data[0]&15) * 4
		total := int(binary.BigEndian.Uint16(data[2:4]))
		if offset < 20 || total < offset || total > len(data) {
			return p, errWGPacket
		}
		data = data[:total]
		p.source = netip.AddrFrom4([4]byte(data[12:16]))
		p.destination = netip.AddrFrom4([4]byte(data[16:20]))
		p.protocol, p.length = data[9], uint64(total)
		frag := binary.BigEndian.Uint16(data[6:8])
		if frag&0x8000 != 0 {
			return p, errWGPacket
		}
		p.more, p.fragmentOffset = frag&0x2000 != 0, uint32(frag&0x1fff)*8
		p.fragmented = p.more || p.fragmentOffset != 0
		p.fragmentID, p.fragmentProtocol = uint32(binary.BigEndian.Uint16(data[4:6])), p.protocol
		p.fragmentLength = uint32(total - offset)
	case 6:
		if len(data) < 40 {
			return p, errWGPacket
		}
		payload := int(binary.BigEndian.Uint16(data[4:6]))
		if payload == 0 {
			return p, errWGUnsupportedPacket
		} // jumbograms
		total := 40 + payload
		if total > len(data) {
			return p, errWGPacket
		}
		data = data[:total]
		p.source = netip.AddrFrom16([16]byte(data[8:24]))
		p.destination = netip.AddrFrom16([16]byte(data[24:40]))
		if p.source.Is4In6() || p.destination.Is4In6() {
			return p, errWGUnsupportedPacket
		}
		p.protocol, p.length, offset = data[6], uint64(total), 40
		for chain := 0; p.protocol != 6 && p.protocol != 17; chain++ {
			if chain >= 8 || offset+2 > len(data) {
				return p, errWGUnsupportedPacket
			}
			next := data[offset]
			switch p.protocol {
			case 0, 43, 60: // hop-by-hop, routing, destination options
				n := (int(data[offset+1]) + 1) * 8
				if offset+n > len(data) {
					return p, errWGPacket
				}
				// Active routing headers change the final destination; do not guess.
				if p.protocol == 43 && data[offset+3] != 0 {
					return p, errWGUnsupportedPacket
				}
				offset += n
			case 44:
				if p.fragmented || offset+8 > len(data) {
					return p, errWGPacket
				}
				bits := binary.BigEndian.Uint16(data[offset+2 : offset+4])
				if bits&6 != 0 {
					return p, errWGPacket
				}
				p.fragmented, p.more = true, bits&1 != 0
				p.fragmentOffset = uint32(bits & 0xfff8)
				p.fragmentID = binary.BigEndian.Uint32(data[offset+4 : offset+8])
				p.fragmentProtocol = next
				offset += 8
				p.fragmentLength = uint32(len(data) - offset)
				p.protocol = next
				if p.fragmentOffset != 0 {
					return p, checkWGFragment(p)
				}
			default: // AH, ESP, mobility, no-next-header and other transports
				return p, errWGUnsupportedPacket
			}
			p.protocol = next
		}
	default:
		return p, errWGUnsupportedPacket
	}
	if p.fragmented {
		if err := checkWGFragment(p); err != nil {
			return p, err
		}
		if p.fragmentOffset != 0 {
			return p, nil
		}
	}
	switch p.protocol {
	case 6:
		if offset+20 > len(data) {
			return p, errWGPacket
		}
		header := int(data[offset+12]>>4) * 4
		if header < 20 || offset+header > len(data) {
			return p, errWGPacket
		}
		p.flags, p.sequence = data[offset+13], binary.BigEndian.Uint32(data[offset+4:offset+8])
	case 17:
		if offset+8 > len(data) {
			return p, errWGPacket
		}
		size := int(binary.BigEndian.Uint16(data[offset+4 : offset+6]))
		if size < 8 || (!p.fragmented && size != len(data)-offset) {
			return p, errWGPacket
		}
	default:
		return p, errWGUnsupportedPacket
	}
	p.sourcePort, p.destinationPort = binary.BigEndian.Uint16(data[offset:offset+2]), binary.BigEndian.Uint16(data[offset+2:offset+4])
	if p.sourcePort == 0 || p.destinationPort == 0 {
		return p, errWGUnsupportedPacket
	}
	return p, nil
}

func checkWGFragment(p wgPacket) error {
	if p.fragmentLength == 0 || (p.more && p.fragmentLength%8 != 0) || p.fragmentOffset+p.fragmentLength > 65535 {
		return errWGPacket
	}
	return nil
}
