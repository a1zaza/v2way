package certificate

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	openapi "github.com/alibabacloud-go/darabonba-openapi/v2/client"
	"github.com/alibabacloud-go/tea/dara"
	alidns "github.com/go-acme/alidns-20150109/v4/client"
	"github.com/go-acme/lego/v4/challenge/dns01"
	tcdns "github.com/go-acme/tencentclouddnspod/v20210323"
	"github.com/linode/linodego"
	"github.com/miekg/dns"
	"github.com/tencentcloud/tencentcloud-sdk-go/tencentcloud/common"
	"github.com/tencentcloud/tencentcloud-sdk-go/tencentcloud/common/profile"
)

type ownedRecord struct {
	ID    string `json:"id"`
	Zone  string `json:"zone"`
	Name  string `json:"name"`
	Value string `json:"value"`
}
type ownedProvider struct {
	records map[string]ownedRecord
	zone    func(string) (string, error)
	create  func(zone, name, value string) (ownedRecord, error)
	remove  func(ownedRecord) error
	persist func(map[string]ownedRecord) error
}

func (p *ownedProvider) Present(domain, token, keyAuth string) error {
	info := dns01.GetChallengeInfo(domain, keyAuth)
	zone, err := p.zone(info.EffectiveFQDN)
	if err != nil {
		return err
	}
	record, err := p.create(strings.TrimSuffix(zone, "."), strings.TrimSuffix(info.EffectiveFQDN, "."), info.Value)
	if err != nil {
		return err
	}
	if record.ID == "" {
		return errors.New("DNS 服务商未返回创建的记录标识")
	}
	p.records[token] = record
	if p.persist != nil {
		return p.persist(p.records)
	}
	return nil
}
func (p *ownedProvider) CleanUp(domain, token, keyAuth string) error {
	record, ok := p.records[token]
	if !ok {
		return nil
	}
	info := dns01.GetChallengeInfo(domain, keyAuth)
	if record.Name != strings.TrimSuffix(info.EffectiveFQDN, ".") || record.Value != info.Value {
		return errors.New("DNS 清理记录与挑战不匹配")
	}
	if err := p.remove(record); err != nil {
		return err
	}
	delete(p.records, token)
	if p.persist != nil {
		return p.persist(p.records)
	}
	return nil
}
func owned(create func(string, string, string) (ownedRecord, error), remove func(ownedRecord) error) *ownedProvider {
	return &ownedProvider{records: map[string]ownedRecord{}, zone: dns01.FindZoneByFqdn, create: create, remove: remove}
}
func relativeName(name, zone string) (string, error) {
	if !strings.HasSuffix(name, "."+zone) {
		return "", errors.New("挑战域名不在所选 DNS 区域内")
	}
	return strings.TrimSuffix(name, "."+zone), nil
}

func newCloudflare(v map[string]string) *ownedProvider {
	api := &cfAPI{base: "https://api.cloudflare.com/client/v4", token: v["apiToken"], zoneToken: v["zoneToken"], client: &http.Client{Timeout: 20 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}}
	return cloudflareOwned(api)
}

type cfAPI struct {
	base, token, zoneToken string
	client                 *http.Client
}

func (c *cfAPI) call(method, path string, input any, out any, zoneRead bool) error {
	var body io.Reader
	if input != nil {
		raw, err := json.Marshal(input)
		if err != nil {
			return err
		}
		body = bytes.NewReader(raw)
	}
	req, err := http.NewRequest(method, c.base+path, body)
	if err != nil {
		return err
	}
	token := c.token
	if zoneRead && c.zoneToken != "" {
		token = c.zoneToken
	}
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Content-Type", "application/json")
	resp, err := c.client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	raw, err := io.ReadAll(io.LimitReader(resp.Body, 2<<20))
	if err != nil {
		return err
	}
	var envelope struct {
		Success bool            `json:"success"`
		Result  json.RawMessage `json:"result"`
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 || json.Unmarshal(raw, &envelope) != nil || !envelope.Success {
		return fmt.Errorf("Cloudflare API 调用失败（HTTP %d），请检查令牌权限和区域", resp.StatusCode)
	}
	if out != nil {
		return json.Unmarshal(envelope.Result, out)
	}
	return nil
}
func cloudflareOwned(api *cfAPI) *ownedProvider {
	return owned(func(zone, name, value string) (ownedRecord, error) {
		var zones []struct {
			ID   string `json:"id"`
			Name string `json:"name"`
		}
		if err := api.call("GET", "/zones?name="+url.QueryEscape(zone), nil, &zones, true); err != nil {
			return ownedRecord{}, err
		}
		zoneID := ""
		for _, z := range zones {
			if z.Name == zone {
				zoneID = z.ID
			}
		}
		if zoneID == "" {
			return ownedRecord{}, errors.New("Cloudflare 未找到可管理的 DNS 区域")
		}
		var result struct {
			ID string `json:"id"`
		}
		err := api.call("POST", "/zones/"+url.PathEscape(zoneID)+"/dns_records", map[string]any{"type": "TXT", "name": name, "content": value, "ttl": 120}, &result, false)
		return ownedRecord{result.ID, zoneID, name, value}, err
	}, func(record ownedRecord) error {
		return api.call("DELETE", "/zones/"+url.PathEscape(record.Zone)+"/dns_records/"+url.PathEscape(record.ID), nil, nil, false)
	})
}
func newTencent(v map[string]string) (*ownedProvider, error) {
	credential := common.NewCredential(v["secretId"], v["secretKey"])
	if v["sessionToken"] != "" {
		credential = common.NewTokenCredential(v["secretId"], v["secretKey"], v["sessionToken"])
	}
	p := profile.NewClientProfile()
	p.HttpProfile.Endpoint = "dnspod.tencentcloudapi.com"
	p.HttpProfile.ReqTimeout = 20
	client, err := tcdns.NewClient(credential, v["region"], p)
	if err != nil {
		return nil, err
	}
	return owned(func(zone, name, value string) (ownedRecord, error) {
		sub, err := relativeName(name, zone)
		if err != nil {
			return ownedRecord{}, err
		}
		req := tcdns.NewCreateRecordRequest()
		req.Domain = common.StringPtr(zone)
		req.SubDomain = common.StringPtr(sub)
		req.RecordType = common.StringPtr("TXT")
		req.RecordLine = common.StringPtr("默认")
		req.Value = common.StringPtr(value)
		req.TTL = common.Uint64Ptr(600)
		resp, err := tcdns.CreateRecordWithContext(context.Background(), client, req)
		if err != nil {
			return ownedRecord{}, err
		}
		if resp == nil || resp.Response == nil || resp.Response.RecordId == nil {
			return ownedRecord{}, errors.New("腾讯云未返回记录 ID")
		}
		return ownedRecord{strconv.FormatUint(*resp.Response.RecordId, 10), zone, name, value}, nil
	}, func(record ownedRecord) error {
		id, err := strconv.ParseUint(record.ID, 10, 64)
		if err != nil {
			return err
		}
		req := tcdns.NewDeleteRecordRequest()
		req.Domain = common.StringPtr(record.Zone)
		req.RecordId = common.Uint64Ptr(id)
		_, err = tcdns.DeleteRecordWithContext(context.Background(), client, req)
		return err
	}), nil
}
func newAli(v map[string]string) (*ownedProvider, error) {
	cfg := new(openapi.Config).SetRegionId("cn-hangzhou").SetAccessKeyId(v["accessKeyId"]).SetAccessKeySecret(v["accessKeySecret"]).SetReadTimeout(20000).SetConnectTimeout(20000)
	if v["securityToken"] != "" {
		cfg = cfg.SetSecurityToken(v["securityToken"])
	}
	client, err := alidns.NewClient(cfg)
	if err != nil {
		return nil, err
	}
	return owned(func(zone, name, value string) (ownedRecord, error) {
		sub, err := relativeName(name, zone)
		if err != nil {
			return ownedRecord{}, err
		}
		req := new(alidns.AddDomainRecordRequest).SetDomainName(zone).SetRR(sub).SetType("TXT").SetValue(value).SetTTL(600)
		resp, err := alidns.AddDomainRecordWithContext(context.Background(), client, req, &dara.RuntimeOptions{})
		if err != nil {
			return ownedRecord{}, err
		}
		if resp == nil || resp.Body == nil || resp.Body.RecordId == nil {
			return ownedRecord{}, errors.New("阿里云未返回记录 ID")
		}
		return ownedRecord{*resp.Body.RecordId, zone, name, value}, nil
	}, func(record ownedRecord) error {
		_, err := alidns.DeleteDomainRecordWithContext(context.Background(), client, new(alidns.DeleteDomainRecordRequest).SetRecordId(record.ID), &dara.RuntimeOptions{})
		return err
	}), nil
}
func newDigitalOcean(v map[string]string) *ownedProvider {
	client := &http.Client{Timeout: 20 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	call := func(method, path string, input any, out any) error {
		var body io.Reader
		if input != nil {
			raw, err := json.Marshal(input)
			if err != nil {
				return err
			}
			body = bytes.NewReader(raw)
		}
		req, err := http.NewRequest(method, "https://api.digitalocean.com/v2"+path, body)
		if err != nil {
			return err
		}
		req.Header.Set("Authorization", "Bearer "+v["apiToken"])
		req.Header.Set("Content-Type", "application/json")
		resp, err := client.Do(req)
		if err != nil {
			return err
		}
		defer resp.Body.Close()
		if resp.StatusCode < 200 || resp.StatusCode >= 300 {
			return fmt.Errorf("DigitalOcean DNS API 返回 HTTP %d", resp.StatusCode)
		}
		if out != nil {
			return json.NewDecoder(io.LimitReader(resp.Body, 2<<20)).Decode(out)
		}
		return nil
	}
	return owned(func(zone, name, value string) (ownedRecord, error) {
		sub, err := relativeName(name, zone)
		if err != nil {
			return ownedRecord{}, err
		}
		var result struct {
			Record struct {
				ID int64 `json:"id"`
			} `json:"domain_record"`
		}
		err = call("POST", "/domains/"+url.PathEscape(zone)+"/records", map[string]any{"type": "TXT", "name": sub, "data": value, "ttl": 120}, &result)
		if err != nil {
			return ownedRecord{}, err
		}
		if result.Record.ID <= 0 {
			return ownedRecord{}, errors.New("DigitalOcean 未返回记录 ID")
		}
		return ownedRecord{strconv.FormatInt(result.Record.ID, 10), zone, name, value}, nil
	}, func(record ownedRecord) error {
		return call("DELETE", "/domains/"+url.PathEscape(record.Zone)+"/records/"+url.PathEscape(record.ID), nil, nil)
	})
}
func newLinode(v map[string]string) *ownedProvider {
	client := linodego.NewClient(&http.Client{Timeout: 20 * time.Second})
	client.SetToken(v["apiToken"]).SetRetryCount(0)
	return owned(func(zone, name, value string) (ownedRecord, error) {
		sub, err := relativeName(name, zone)
		if err != nil {
			return ownedRecord{}, err
		}
		filter, _ := json.Marshal(map[string]string{"domain": zone})
		domains, err := client.ListDomains(context.Background(), linodego.NewListOptions(0, string(filter)))
		if err != nil {
			return ownedRecord{}, err
		}
		zoneID := 0
		for _, d := range domains {
			if d.Domain == zone {
				zoneID = d.ID
			}
		}
		if zoneID == 0 {
			return ownedRecord{}, errors.New("Linode 未找到可管理的域名")
		}
		record, err := client.CreateDomainRecord(context.Background(), zoneID, linodego.DomainRecordCreateOptions{Type: linodego.RecordTypeTXT, Name: sub, Target: value, TTLSec: 300})
		if err != nil {
			return ownedRecord{}, err
		}
		return ownedRecord{strconv.Itoa(record.ID), strconv.Itoa(zoneID), name, value}, nil
	}, func(record ownedRecord) error {
		domainID, err := strconv.Atoi(record.Zone)
		if err != nil {
			return err
		}
		id, err := strconv.Atoi(record.ID)
		if err != nil {
			return err
		}
		return client.DeleteDomainRecord(context.Background(), domainID, id)
	})
}

type rfcProvider struct{ server, zone, key, secret, algorithm string }

func newRFC2136(v map[string]string) *rfcProvider {
	server := v["nameserver"]
	if _, _, err := net.SplitHostPort(server); err != nil {
		server = net.JoinHostPort(server, "53")
	}
	algorithm := v["tsigAlgorithm"]
	if algorithm == "" {
		algorithm = "hmac-sha256"
	}
	return &rfcProvider{server, dns.Fqdn(v["zone"]), dns.Fqdn(v["tsigKey"]), v["tsigSecret"], dns.Fqdn(algorithm)}
}
func (p *rfcProvider) Present(domain, token, keyAuth string) error {
	return p.update(domain, keyAuth, false)
}
func (p *rfcProvider) CleanUp(domain, token, keyAuth string) error {
	return p.update(domain, keyAuth, true)
}
func (p *rfcProvider) update(domain, keyAuth string, remove bool) error {
	info := dns01.GetChallengeInfo(domain, keyAuth)
	return p.updateValue(info.EffectiveFQDN, info.Value, remove)
}
func (p *rfcProvider) updateValue(name, value string, remove bool) error {
	if !dns.IsSubDomain(p.zone, dns.Fqdn(name)) {
		return errors.New("挑战记录不在配置的 DNS 区域内")
	}
	record := &dns.TXT{Hdr: dns.RR_Header{Name: dns.Fqdn(name), Rrtype: dns.TypeTXT, Class: dns.ClassINET, Ttl: 120}, Txt: []string{value}}
	msg := new(dns.Msg)
	msg.SetUpdate(p.zone)
	if remove {
		msg.Remove([]dns.RR{record})
	} else {
		msg.Insert([]dns.RR{record})
	}
	msg.SetTsig(p.key, p.algorithm, 300, time.Now().Unix())
	client := &dns.Client{Net: "tcp", Timeout: 10 * time.Second, TsigSecret: map[string]string{p.key: p.secret}}
	reply, _, err := client.Exchange(msg, p.server)
	if err != nil {
		return err
	}
	if reply.Rcode != dns.RcodeSuccess {
		return fmt.Errorf("DNS 动态更新失败：%s", dns.RcodeToString[reply.Rcode])
	}
	return nil
}
func newRFC2136Owned(v map[string]string) *ownedProvider {
	rfc := newRFC2136(v)
	p := owned(func(zone, name, value string) (ownedRecord, error) {
		if err := rfc.updateValue(name, value, false); err != nil {
			return ownedRecord{}, err
		}
		sum := sha256.Sum256([]byte(name + "\n" + value))
		return ownedRecord{hex.EncodeToString(sum[:]), zone, name, value}, nil
	}, func(record ownedRecord) error { return rfc.updateValue(record.Name, record.Value, true) })
	p.zone = func(string) (string, error) { return rfc.zone, nil }
	return p
}
