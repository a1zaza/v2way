package certificate

import (
	"context"
	"encoding/base64"
	"errors"
	"net"
	"strings"
	"time"

	"github.com/go-acme/lego/v4/challenge"
	"github.com/go-acme/lego/v4/challenge/dns01"
	"github.com/miekg/dns"
	"v2way/internal/config"
)

type CredentialField struct {
	Key      string `json:"key"`
	Label    string `json:"label"`
	Secret   bool   `json:"secret"`
	Required bool   `json:"required"`
}
type DNSProviderInfo struct {
	ID          string            `json:"id"`
	Name        string            `json:"name"`
	Description string            `json:"description"`
	Fields      []CredentialField `json:"fields"`
}

func DNSProviders() []DNSProviderInfo {
	return []DNSProviderInfo{
		{"cloudflare", "Cloudflare", "令牌需要目标区域的 Zone:Read 与 DNS:Edit 权限。", []CredentialField{{"apiToken", "DNS API 令牌", true, true}, {"zoneToken", "区域读取令牌（可选）", true, false}}},
		{"tencentcloud", "腾讯云 DNSPod", "使用腾讯云 API SecretId / SecretKey，不是旧版 DNSPod 登录令牌。", []CredentialField{{"secretId", "SecretId", true, true}, {"secretKey", "SecretKey", true, true}, {"region", "区域（可选）", false, false}, {"sessionToken", "临时会话令牌（可选）", true, false}}},
		{"alidns", "阿里云 DNS", "使用具有目标域名解析权限的 RAM 凭据。", []CredentialField{{"accessKeyId", "AccessKey ID", true, true}, {"accessKeySecret", "AccessKey Secret", true, true}, {"securityToken", "STS 令牌（可选）", true, false}}},
		{"digitalocean", "DigitalOcean", "需要管理目标域名解析的 API 令牌。", []CredentialField{{"apiToken", "API 令牌", true, true}}},
		{"linode", "Linode / Akamai", "需要目标域名的读取和写入权限。", []CredentialField{{"apiToken", "API 令牌", true, true}}},
		{"rfc2136", "RFC 2136 / 自建 DNS", "适用于 BIND、Knot 等支持 TSIG 动态更新的权威 DNS。", []CredentialField{{"nameserver", "更新服务器，如 dns.example.com:53", false, true}, {"zone", "DNS 区域，如 example.com", false, true}, {"tsigKey", "TSIG 密钥名称", false, true}, {"tsigSecret", "TSIG 密钥（Base64）", true, true}, {"tsigAlgorithm", "签名算法（可选，默认 hmac-sha256）", false, false}}},
	}
}
func validateCredentials(provider string, values map[string]string) error {
	for _, p := range DNSProviders() {
		if p.ID != provider {
			continue
		}
		allowed := map[string]bool{}
		for _, field := range p.Fields {
			allowed[field.Key] = true
			if field.Required && strings.TrimSpace(values[field.Key]) == "" {
				return errors.New("缺少 DNS 凭据字段：" + field.Label)
			}
		}
		for key, value := range values {
			if !allowed[key] || len(value) > 4096 || strings.ContainsAny(value, "\r\n") {
				return errors.New("DNS 凭据字段未知、过长或含换行")
			}
		}
		if provider == "rfc2136" {
			if _, err := base64.StdEncoding.DecodeString(values["tsigSecret"]); err != nil {
				return errors.New("TSIG 密钥不是有效 Base64")
			}
			zone, err := config.Domain(values["zone"])
			if err != nil || strings.HasPrefix(zone, "*.") {
				return errors.New("DNS 区域无效")
			}
			if alg := values["tsigAlgorithm"]; alg != "" && alg != "hmac-sha256" && alg != "hmac-sha512" {
				return errors.New("TSIG 算法仅支持 hmac-sha256 或 hmac-sha512")
			}
		}
		return nil
	}
	return errors.New("不支持的 DNS 服务商")
}

// Explicit configs prevent ambient environment credentials or endpoints from
// leaking across accounts. Never mutate process-wide environment variables.
func newDNSProvider(provider string, v map[string]string) (challenge.Provider, error) {
	if err := validateCredentials(provider, v); err != nil {
		return nil, err
	}
	switch provider {
	case "cloudflare":
		return newCloudflare(v), nil
	case "tencentcloud":
		return newTencent(v)
	case "alidns":
		return newAli(v)
	case "digitalocean":
		return newDigitalOcean(v), nil
	case "linode":
		return newLinode(v), nil
	case "rfc2136":
		return newRFC2136Owned(v), nil
	}
	return nil, errors.New("不支持的 DNS 服务商")
}
func waitDNS(ctx context.Context, domain, keyAuth string) error {
	info := dns01.GetChallengeInfo(domain, keyAuth)
	ticker := time.NewTicker(5 * time.Second)
	defer ticker.Stop()
	for {
		if dnsVisible(ctx, info.EffectiveFQDN, info.Value) {
			return nil
		}
		select {
		case <-ctx.Done():
			return errors.New("等待 DNS TXT 记录传播超时，请检查解析权限与权威 DNS")
		case <-ticker.C:
		}
	}
}
func dnsVisible(ctx context.Context, fqdn, value string) bool {
	zone, err := dns01.FindZoneByFqdn(fqdn)
	if err != nil {
		return false
	}
	servers, err := net.DefaultResolver.LookupNS(ctx, zone)
	if err != nil || len(servers) == 0 {
		return false
	}
	for _, server := range servers {
		q := new(dns.Msg)
		q.SetQuestion(dns.Fqdn(fqdn), dns.TypeTXT)
		q.RecursionDesired = false
		client := &dns.Client{Timeout: 4 * time.Second}
		reply, _, err := client.ExchangeContext(ctx, q, net.JoinHostPort(server.Host, "53"))
		if err != nil {
			return false
		}
		if reply.Truncated {
			client.Net = "tcp"
			reply, _, err = client.ExchangeContext(ctx, q, net.JoinHostPort(server.Host, "53"))
			if err != nil {
				return false
			}
		}
		found := false
		for _, record := range reply.Answer {
			if txt, ok := record.(*dns.TXT); ok && strings.EqualFold(txt.Hdr.Name, dns.Fqdn(fqdn)) && strings.Join(txt.Txt, "") == value {
				found = true
			}
		}
		if !found {
			return false
		}
	}
	return true
}
