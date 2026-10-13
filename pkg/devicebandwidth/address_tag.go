package devicebandwidth

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"net/netip"

	"github.com/google/uuid"
)

// AddressOutboundTag names a server-controlled source-address cohort. The
// credential UUID remains the SOCKS username and budget identity; only the
// local password and outbound tag differ. It is not a client identity header.
func AddressOutboundTag(credential, source string) (string, string, error) {
	id, err := uuid.Parse(credential)
	if err != nil {
		return "", "", fmt.Errorf("invalid credential UUID")
	}
	address, err := netip.ParseAddr(source)
	if err != nil || address.IsUnspecified() || address.IsMulticast() || address.Zone() != "" {
		return "", "", fmt.Errorf("invalid source address")
	}
	address = address.Unmap()
	prefix := netip.PrefixFrom(address, address.BitLen()).String()
	sum := sha256.Sum256([]byte(prefix))
	return OutboundPrefix + id.String() + "~" + hex.EncodeToString(sum[:]), prefix, nil
}
