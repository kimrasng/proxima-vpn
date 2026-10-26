package handlers

import (
	"strings"

	"github.com/gofiber/fiber/v2"
	"github.com/proximavpn/proxima-vpn/api-server/internal/services"
	"github.com/proximavpn/proxima-vpn/pkg/useragent"
)

func promoVisitor(c *fiber.Ctx) services.PromoVisitor {
	country := ""
	if c.IsProxyTrusted() {
		country = strings.ToUpper(strings.TrimSpace(c.Get("CF-IPCountry")))
	}
	return services.PromoVisitor{
		Country: country,
		Browser: string(useragent.Classify(c.Get("User-Agent")).Browser),
		IP:      c.IP(),
	}
}
