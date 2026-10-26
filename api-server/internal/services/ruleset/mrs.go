package ruleset

import (
	"bytes"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"math/bits"
	"net/netip"
	"slices"
	"strings"

	"github.com/klauspost/compress/zstd"
)

// mrs is mihomo's binary rule-set format: zstd( "MRS\x01" | behavior byte |
// count int64 | extra-len int64 | extra | body ). The body is a succinct
// domain trie for domain sets and a list of IP ranges for ipcidr sets; see
// mihomo rules/provider/mrs_reader.go and component/{trie,cidr}.

var mrsMagic = [4]byte{'M', 'R', 'S', 1}

const (
	mrsBehaviorDomain = 0
	mrsBehaviorIPCIDR = 1

	// maxMRSDecoded bounds the decompressed size so a hostile URL cannot
	// exhaust memory; the largest MetaCubeX set (geosite cn) is well below it.
	maxMRSDecoded = 64 << 20
)

// DecodeMRS returns the behavior ("domain" or "ipcidr"), the entry count the
// file declares, and its entries rendered the way mihomo's mrs dump renders
// them: domains as "+.example.com" when both the domain and its subdomains
// match, IP ranges as CIDR prefixes.
func DecodeMRS(data []byte) (behavior string, count int64, entries []string, err error) {
	dec, err := zstd.NewReader(bytes.NewReader(data), zstd.WithDecoderMaxMemory(maxMRSDecoded))
	if err != nil {
		return "", 0, nil, err
	}
	defer dec.Close()
	r := io.LimitReader(dec, maxMRSDecoded)

	var header [4]byte
	if _, err := io.ReadFull(r, header[:]); err != nil {
		return "", 0, nil, fmt.Errorf("read header: %w", err)
	}
	if header != mrsMagic {
		return "", 0, nil, errors.New("not an MRS v1 file")
	}
	var b [1]byte
	if _, err := io.ReadFull(r, b[:]); err != nil {
		return "", 0, nil, err
	}
	if err := binary.Read(r, binary.BigEndian, &count); err != nil {
		return "", 0, nil, err
	}
	var extra int64
	if err := binary.Read(r, binary.BigEndian, &extra); err != nil {
		return "", 0, nil, err
	}
	if extra < 0 || extra > maxMRSDecoded {
		return "", 0, nil, errors.New("invalid extra length")
	}
	if _, err := io.CopyN(io.Discard, r, extra); err != nil {
		return "", 0, nil, err
	}

	switch b[0] {
	case mrsBehaviorDomain:
		entries, err = readDomainSet(r)
		return "domain", count, entries, err
	case mrsBehaviorIPCIDR:
		entries, err = readIPCIDRSet(r)
		return "ipcidr", count, entries, err
	default:
		return "", 0, nil, fmt.Errorf("unsupported MRS behavior %d", b[0])
	}
}

func readLength(r io.Reader, limit int64) (int64, error) {
	var n int64
	if err := binary.Read(r, binary.BigEndian, &n); err != nil {
		return 0, err
	}
	if n < 1 || n > limit {
		return 0, errors.New("invalid length")
	}
	return n, nil
}

func readUint64s(r io.Reader) ([]uint64, error) {
	n, err := readLength(r, maxMRSDecoded/8)
	if err != nil {
		return nil, err
	}
	out := make([]uint64, n)
	if err := binary.Read(r, binary.BigEndian, out); err != nil {
		return nil, err
	}
	return out, nil
}

// readDomainSet walks a succinct trie (LOUDS-style label bitmap) whose keys
// are reversed domains, a trailing '+' marking a subdomain wildcard.
func readDomainSet(r io.Reader) ([]string, error) {
	var version [1]byte
	if _, err := io.ReadFull(r, version[:]); err != nil {
		return nil, err
	}
	if version[0] != 1 {
		return nil, errors.New("unsupported domain set version")
	}
	leaves, err := readUint64s(r)
	if err != nil {
		return nil, fmt.Errorf("leaves: %w", err)
	}
	labelBitmap, err := readUint64s(r)
	if err != nil {
		return nil, fmt.Errorf("label bitmap: %w", err)
	}
	n, err := readLength(r, maxMRSDecoded)
	if err != nil {
		return nil, fmt.Errorf("labels: %w", err)
	}
	labels := make([]byte, n)
	if _, err := io.ReadFull(r, labels); err != nil {
		return nil, err
	}

	getBit := func(bm []uint64, i int) bool {
		w := i >> 6
		return w < len(bm) && bm[w]&(1<<uint(i&63)) != 0
	}
	// ones[i] = bit index of the i-th set bit in labelBitmap (0-based).
	var ones []int
	for w, word := range labelBitmap {
		for word != 0 {
			t := bits.TrailingZeros64(word)
			ones = append(ones, w*64+t)
			word &= word - 1
		}
	}

	var keys []string
	var current []byte
	var traverse func(nodeID, bmIdx, depth int) error
	traverse = func(nodeID, bmIdx, depth int) error {
		if depth > 512 {
			return errors.New("trie too deep")
		}
		if getBit(leaves, nodeID) {
			keys = append(keys, string(current))
		}
		for ; ; bmIdx++ {
			if bmIdx>>6 >= len(labelBitmap) {
				return errors.New("truncated label bitmap")
			}
			if getBit(labelBitmap, bmIdx) {
				return nil
			}
			li := bmIdx - nodeID
			if li < 0 || li >= len(labels) {
				return errors.New("label index out of range")
			}
			current = append(current, labels[li])
			next := bmIdx - nodeID + 1
			if next-1 < 0 || next-1 >= len(ones) {
				return errors.New("node index out of range")
			}
			if err := traverse(next, ones[next-1]+1, depth+1); err != nil {
				return err
			}
			current = current[:len(current)-1]
		}
	}
	if err := traverse(0, 0, 0); err != nil {
		return nil, err
	}

	domains := make([]string, 0, len(keys))
	for _, k := range keys {
		domains = append(domains, reverseString(strings.TrimSuffix(k, "+")))
	}
	return compactDomains(domains), nil
}

// compactDomains folds "example.com" + ".example.com" into "+.example.com",
// as mihomo's DumpMrs does, then sorts.
func compactDomains(keys []string) []string {
	slices.SortFunc(keys, func(a, b string) int {
		return strings.Compare(strings.TrimPrefix(a, "."), strings.TrimPrefix(b, "."))
	})
	out := make([]string, 0, len(keys))
	for i := 0; i < len(keys); i++ {
		d := strings.TrimPrefix(keys[i], ".")
		if i+1 < len(keys) && d == strings.TrimPrefix(keys[i+1], ".") && keys[i] != keys[i+1] {
			out = append(out, "+."+d)
			i++
			continue
		}
		out = append(out, keys[i])
	}
	slices.Sort(out)
	return out
}

func reverseString(s string) string {
	b := []byte(s)
	for i, j := 0, len(b)-1; i < j; i, j = i+1, j-1 {
		b[i], b[j] = b[j], b[i]
	}
	return string(b)
}

func readIPCIDRSet(r io.Reader) ([]string, error) {
	var version [1]byte
	if _, err := io.ReadFull(r, version[:]); err != nil {
		return nil, err
	}
	if version[0] != 1 {
		return nil, errors.New("unsupported ipcidr set version")
	}
	n, err := readLength(r, maxMRSDecoded/32)
	if err != nil {
		return nil, err
	}
	var out []string
	for i := int64(0); i < n; i++ {
		var from, to [16]byte
		if _, err := io.ReadFull(r, from[:]); err != nil {
			return nil, err
		}
		if _, err := io.ReadFull(r, to[:]); err != nil {
			return nil, err
		}
		a, b := netip.AddrFrom16(from).Unmap(), netip.AddrFrom16(to).Unmap()
		for _, p := range rangeToPrefixes(a, b) {
			out = append(out, p.String())
		}
	}
	return out, nil
}

// rangeToPrefixes splits an inclusive address range into the minimal list of
// CIDR prefixes covering it.
func rangeToPrefixes(from, to netip.Addr) []netip.Prefix {
	var out []netip.Prefix
	for from.IsValid() && from.Compare(to) <= 0 {
		bitsLen := from.BitLen()
		size := bitsLen
		for size > 0 {
			p, err := from.Prefix(size - 1)
			if err != nil || p.Addr() != from || lastAddr(p).Compare(to) > 0 {
				break
			}
			size--
		}
		p := netip.PrefixFrom(from, size)
		out = append(out, p)
		last := lastAddr(p)
		if last == to || !last.Next().IsValid() {
			break
		}
		from = last.Next()
		if len(out) > 1<<20 {
			break
		}
	}
	return out
}

func lastAddr(p netip.Prefix) netip.Addr {
	a := p.Masked().Addr().AsSlice()
	hostBits := p.Addr().BitLen() - p.Bits()
	for i := len(a) - 1; i >= 0 && hostBits > 0; i-- {
		if hostBits >= 8 {
			a[i] = 0xff
			hostBits -= 8
		} else {
			a[i] |= byte(1<<uint(hostBits)) - 1
			hostBits = 0
		}
	}
	out, _ := netip.AddrFromSlice(a)
	return out
}
