package certificate

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/sha256"
	"crypto/x509"
	"encoding/hex"
	"encoding/json"
	"encoding/pem"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/mail"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/go-acme/lego/v4/challenge"
	"golang.org/x/crypto/acme"
	"v2way/internal/config"
	"v2way/internal/jsonfile"
	"v2way/internal/store"
)

const ProductionDirectory = "https://acme-v02.api.letsencrypt.org/directory"
const StagingDirectory = "https://acme-staging-v02.api.letsencrypt.org/directory"

type IssueRequest struct {
	RequestID   string            `json:"requestId,omitempty"`
	Name        string            `json:"name"`
	Domains     []string          `json:"domains"`
	Email       string            `json:"email"`
	Challenge   string            `json:"challenge"`
	Environment string            `json:"environment"`
	DNSProvider string            `json:"dnsProvider,omitempty"`
	Credentials map[string]string `json:"credentials,omitempty"`
	AcceptTerms bool              `json:"acceptTerms"`
	AutoRenew   bool              `json:"autoRenew"`
}
type Job struct {
	RequestID     string    `json:"requestId,omitempty"`
	ID            string    `json:"id"`
	CertificateID string    `json:"certificateId"`
	Name          string    `json:"name"`
	Domains       []string  `json:"domains"`
	Challenge     string    `json:"challenge"`
	DNSProvider   string    `json:"dnsProvider,omitempty"`
	Environment   string    `json:"environment"`
	Status        string    `json:"status"`
	Message       string    `json:"message"`
	CreatedAt     time.Time `json:"createdAt"`
	UpdatedAt     time.Time `json:"updatedAt"`
	NextAttempt   time.Time `json:"nextAttempt,omitempty"`
	Failures      int       `json:"failures"`
}
type plan struct {
	SchemaVersion int          `json:"schemaVersion"`
	Request       IssueRequest `json:"request"`
}
type acmeAccount struct {
	PrivateKey []byte `json:"privateKey"`
	URI        string `json:"uri"`
	Email      string `json:"email"`
	Directory  string `json:"directory"`
}
type issuanceAttempt struct {
	PrivateKey []byte                 `json:"privateKey"`
	OrderURL   string                 `json:"orderUrl"`
	Records    map[string]ownedRecord `json:"records,omitempty"`
}
type uncertainIssue struct{ err error }

func (e *uncertainIssue) Error() string { return "签发结果待确认：" + e.err.Error() }
func (e *uncertainIssue) Unwrap() error { return e.err }

type httpToken struct {
	Response string
	Expires  time.Time
}
type queuedJob struct {
	Job     Job
	Request IssueRequest
}
type Automation struct {
	manager   *Manager
	mu        sync.Mutex
	jobs      map[string]Job
	running   map[string]bool
	tokens    map[string]httpToken
	queue     chan queuedJob
	ctx       context.Context
	cancel    context.CancelFunc
	wg        sync.WaitGroup
	start     sync.Once
	directory func(string) string
	client    *http.Client
	provider  func(string, map[string]string) (challenge.Provider, error)
	waitDNS   func(context.Context, string, string) error
}

func newAutomation(m *Manager) (*Automation, error) {
	ctx, cancel := context.WithCancel(context.Background())
	a := &Automation{manager: m, jobs: map[string]Job{}, running: map[string]bool{}, tokens: map[string]httpToken{}, queue: make(chan queuedJob, 16), ctx: ctx, cancel: cancel, client: &http.Client{Timeout: 45 * time.Second}, provider: newDNSProvider, waitDNS: waitDNS, directory: func(env string) string {
		if env == "staging" {
			return StagingDirectory
		}
		return ProductionDirectory
	}}
	entries, err := os.ReadDir(filepath.Join(m.dir, "acme", "jobs"))
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		cancel()
		return nil, err
	}
	for _, entry := range entries {
		if !safeID.MatchString(strings.TrimSuffix(entry.Name(), ".json")) || !strings.HasSuffix(entry.Name(), ".json") {
			continue
		}
		var j Job
		if err = jsonfile.Read(filepath.Join(m.dir, "acme", "jobs", entry.Name()), &j); err != nil {
			cancel()
			return nil, errors.New("ACME 任务记录损坏")
		}
		if j.ID != strings.TrimSuffix(entry.Name(), ".json") || !safeID.MatchString(j.CertificateID) {
			cancel()
			return nil, errors.New("ACME 任务标识损坏")
		}
		if j.Status == "queued" || j.Status == "running" {
			j.Status = "interrupted"
			j.Message = "进程中断，订单结果未确认，请检查后重试"
			j.UpdatedAt = time.Now()
			if err = a.persist(j); err != nil {
				cancel()
				return nil, err
			}
		}
		a.jobs[j.ID] = j
	}
	return a, nil
}
func (m *Manager) Automation() *Automation { return m.auto }
func (a *Automation) Start()               { a.start.Do(func() { a.wg.Add(2); go a.worker(); go a.scheduler() }) }
func (a *Automation) Close()               { a.cancel(); a.wg.Wait() }
func (a *Automation) persist(j Job) error {
	if !safeID.MatchString(j.ID) || !safeID.MatchString(j.CertificateID) {
		return errors.New("ACME 任务标识无效")
	}
	return jsonfile.Write(filepath.Join(a.manager.dir, "acme", "jobs", j.ID+".json"), j)
}
func normalizeRequest(r IssueRequest) (IssueRequest, error) {
	if r.RequestID != "" && !safeID.MatchString(r.RequestID) {
		return r, errors.New("申请请求标识无效")
	}
	r.Name = strings.TrimSpace(r.Name)
	if r.Name == "" || len(r.Name) > 128 {
		return r, errors.New("证书名称应为 1–128 字节")
	}
	if len(r.Domains) == 0 || len(r.Domains) > 50 {
		return r, errors.New("证书需包含 1–50 个域名")
	}
	email, err := mail.ParseAddress(r.Email)
	if err != nil || email.Address != r.Email || strings.ContainsAny(r.Email, "\r\n") {
		return r, errors.New("请填写有效的联系邮箱")
	}
	if r.Environment != "production" && r.Environment != "staging" {
		return r, errors.New("请选择正式或测试 CA")
	}
	if !r.AcceptTerms {
		return r, errors.New("申请前必须同意 Let’s Encrypt 服务条款")
	}
	if r.Challenge != "http-01" && r.Challenge != "dns-01" {
		return r, errors.New("请选择 HTTP-01 或 DNS-01")
	}
	seen := map[string]bool{}
	for i, raw := range r.Domains {
		d, e := config.Domain(raw)
		if e != nil || net.ParseIP(d) != nil || !strings.Contains(d, ".") || d == "localhost" {
			return r, errors.New("ACME 申请仅支持有效公开域名")
		}
		if strings.HasPrefix(d, "*.") && r.Challenge != "dns-01" {
			return r, errors.New("通配符证书需要 DNS-01 验证")
		}
		if seen[d] {
			return r, errors.New("证书域名重复")
		}
		seen[d] = true
		r.Domains[i] = d
	}
	sort.Strings(r.Domains)
	if r.Challenge == "dns-01" {
		if err = validateCredentials(r.DNSProvider, r.Credentials); err != nil {
			return r, err
		}
	} else {
		r.DNSProvider = ""
		r.Credentials = nil
	}
	return r, nil
}
func (a *Automation) Issue(r IssueRequest) (Job, error) {
	r, err := normalizeRequest(r)
	if err != nil {
		return Job{}, err
	}
	id := store.Token()[:24]
	a.mu.Lock()
	defer a.mu.Unlock()
	if r.RequestID != "" {
		for _, previous := range a.jobs {
			if previous.RequestID == r.RequestID {
				var stored plan
				if err = a.manager.readSecret("plans", previous.CertificateID, &stored); err != nil {
					return Job{}, errors.New("原申请配置无法读取，不能重复提交")
				}
				before, _ := json.Marshal(stored.Request)
				after, _ := json.Marshal(r)
				if string(before) != string(after) {
					return Job{}, errors.New("相同请求标识已用于不同的签发参数，请先检查原任务")
				}
				return previous, nil
			}
		}
	} else {
		r.RequestID = store.Token()[:24]
	}
	entries, err := os.ReadDir(filepath.Join(a.manager.dir, "acme", "plans"))
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return Job{}, err
	}
	if len(entries) >= 1000 {
		return Job{}, errors.New("自动证书配置已达 1000 项上限")
	}
	if len(a.queue) >= cap(a.queue) {
		return Job{}, errors.New("签发队列已满，请稍后重试")
	}
	if err = a.manager.writeSecret("plans", id, plan{1, r}); err != nil {
		return Job{}, errors.New("无法保存加密签发配置")
	}
	return a.enqueueLocked(id, r, 0)
}
func (a *Automation) Renew(id string) (Job, error) {
	if !safeID.MatchString(id) {
		return Job{}, errors.New("证书标识无效")
	}
	var p plan
	if err := a.manager.readSecret("plans", id, &p); err != nil {
		return Job{}, errors.New("自动签发配置不存在或无法解密")
	}
	if p.SchemaVersion != 1 {
		return Job{}, errors.New("自动签发配置版本无效")
	}
	r, err := normalizeRequest(p.Request)
	if err != nil {
		return Job{}, err
	}
	r.RequestID = ""
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.enqueueLocked(id, r, a.failuresLocked(id))
}
func (a *Automation) failuresLocked(id string) int {
	var latest Job
	for _, j := range a.jobs {
		if j.CertificateID == id && j.UpdatedAt.After(latest.UpdatedAt) {
			latest = j
		}
	}
	return latest.Failures
}
func (a *Automation) enqueueLocked(id string, r IssueRequest, failures int) (Job, error) {
	select {
	case <-a.ctx.Done():
		return Job{}, errors.New("证书服务已停止")
	default:
	}
	if a.running[id] {
		return Job{}, errors.New("该证书已有任务正在执行")
	}
	if len(a.queue) >= cap(a.queue) {
		return Job{}, errors.New("签发队列已满，请稍后重试")
	}
	// A single worker also serializes overlapping wildcard/apex TXT records.
	j := Job{ID: store.Token()[:24], RequestID: r.RequestID, CertificateID: id, Name: r.Name, Domains: append([]string{}, r.Domains...), Challenge: r.Challenge, DNSProvider: r.DNSProvider, Environment: r.Environment, Status: "queued", Message: "等待签发", CreatedAt: time.Now().UTC(), UpdatedAt: time.Now().UTC(), Failures: failures}
	var latest Job
	for _, previous := range a.jobs {
		if previous.CertificateID == id && previous.UpdatedAt.After(latest.UpdatedAt) {
			latest = previous
		}
	}
	if latest.Status == "unknown" || latest.Status == "interrupted" {
		var attempt issuanceAttempt
		if err := a.manager.readSecret("attempts", latest.ID, &attempt); err == nil {
			if err = a.manager.writeSecret("attempts", j.ID, attempt); err != nil {
				return Job{}, errors.New("无法保存订单恢复材料")
			}
		} else if !errors.Is(err, os.ErrNotExist) {
			return Job{}, errors.New("订单恢复材料损坏，不能自动创建新订单")
		}
	}
	if err := a.persist(j); err != nil {
		return Job{}, errors.New("无法保存签发任务")
	}
	a.jobs[j.ID] = j
	a.running[id] = true
	a.queue <- queuedJob{j, r}
	return j, nil
}
func (a *Automation) Jobs() []Job {
	a.mu.Lock()
	defer a.mu.Unlock()
	out := make([]Job, 0, len(a.jobs))
	for _, j := range a.jobs {
		j.Domains = append([]string{}, j.Domains...)
		out = append(out, j)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].CreatedAt.After(out[j].CreatedAt) })
	if len(out) > 200 {
		out = out[:200]
	}
	return out
}
func (a *Automation) progress(id, message string) {
	a.mu.Lock()
	defer a.mu.Unlock()
	j := a.jobs[id]
	j.Status = "running"
	j.Message = message
	j.UpdatedAt = time.Now().UTC()
	a.jobs[id] = j
	_ = a.persist(j)
}
func (a *Automation) worker() {
	defer a.wg.Done()
	for {
		select {
		case <-a.ctx.Done():
			return
		case q := <-a.queue:
			a.run(q)
		}
	}
}
func (a *Automation) run(q queuedJob) {
	a.progress(q.Job.ID, "创建 ACME 订单")
	ctx, cancel := context.WithTimeout(a.ctx, 15*time.Minute)
	defer cancel()
	err := a.obtain(ctx, q)
	a.mu.Lock()
	defer a.mu.Unlock()
	j := a.jobs[q.Job.ID]
	j.UpdatedAt = time.Now().UTC()
	if err == nil {
		j.Status = "succeeded"
		j.Message = "证书已签发并保存"
		j.Failures = 0
	} else {
		j.Status = "failed"
		j.Message = redactError(err, q.Request.Credentials)
		j.Failures++
		var unknown *uncertainIssue
		if errors.As(err, &unknown) || errors.Is(err, jsonfile.ErrUncertain) {
			j.Status = "unknown"
		}
		delay := time.Duration(1<<min(j.Failures, 5)) * 30 * time.Minute
		var caErr *acme.Error
		errors.As(err, &caErr)
		if caErr != nil {
			if retry, ok := acme.RateLimit(caErr); ok && retry > delay {
				delay = retry
			}
		}
		j.NextAttempt = time.Now().Add(delay)
		if errors.Is(err, context.Canceled) {
			j.Status = "interrupted"
			j.Message = "任务已停止，订单结果可能尚未确认"
		}
	}
	if saveErr := a.persist(j); saveErr != nil {
		j.Message += "；任务状态落盘失败，请检查数据目录"
	}
	a.jobs[j.ID] = j
	delete(a.running, j.CertificateID)
	a.pruneLocked()
}
func (a *Automation) pruneLocked() {
	latest := map[string]Job{}
	all := make([]Job, 0, len(a.jobs))
	for _, j := range a.jobs {
		if j.UpdatedAt.After(latest[j.CertificateID].UpdatedAt) {
			latest[j.CertificateID] = j
		}
		all = append(all, j)
	}
	sort.Slice(all, func(i, j int) bool { return all[i].UpdatedAt.After(all[j].UpdatedAt) })
	for i, j := range all {
		if i < 200 || j.RequestID != "" || latest[j.CertificateID].ID == j.ID || j.Status == "running" || j.Status == "queued" {
			continue
		}
		if err := os.Remove(filepath.Join(a.manager.dir, "acme", "jobs", j.ID+".json")); err == nil || errors.Is(err, os.ErrNotExist) {
			delete(a.jobs, j.ID)
			_ = os.Remove(filepath.Join(a.manager.dir, "acme", "attempts", j.ID+".json"))
		}
	}
}
func redactError(err error, credentials map[string]string) string {
	s := err.Error()
	values := []string{}
	for _, v := range credentials {
		if v != "" {
			values = append(values, v)
		}
	}
	sort.Slice(values, func(i, j int) bool { return len(values[i]) > len(values[j]) })
	for _, v := range values {
		encoded, _ := json.Marshal(v)
		for _, form := range []string{v, url.QueryEscape(v), url.PathEscape(v), strings.Trim(string(encoded), "\"")} {
			if form != "" {
				s = strings.ReplaceAll(s, form, "[凭据已隐藏]")
			}
		}
	}
	if strings.Contains(s, "-----BEGIN") {
		return "签发失败，敏感诊断已隐藏"
	}
	runes := []rune(s)
	if len(runes) > 500 {
		s = string(runes[:500]) + "…"
	}
	return s
}
func (a *Automation) account(ctx context.Context, r IssueRequest) (*acme.Client, error) {
	directory := a.directory(r.Environment)
	sum := sha256.Sum256([]byte(directory + "\n" + r.Email))
	id := hex.EncodeToString(sum[:])[:24]
	var saved acmeAccount
	err := a.manager.readSecret("accounts", id, &saved)
	if errors.Is(err, os.ErrNotExist) {
		key, e := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
		if e != nil {
			return nil, e
		}
		encoded, e := x509.MarshalPKCS8PrivateKey(key)
		if e != nil {
			return nil, e
		}
		saved = acmeAccount{PrivateKey: encoded, Email: r.Email, Directory: directory}
		if e = a.manager.writeSecret("accounts", id, saved); e != nil {
			return nil, e
		}
	} else if err != nil {
		return nil, err
	}
	parsed, err := x509.ParsePKCS8PrivateKey(saved.PrivateKey)
	if err != nil {
		return nil, errors.New("ACME 账户密钥损坏")
	}
	key, ok := parsed.(*ecdsa.PrivateKey)
	if !ok {
		return nil, errors.New("ACME 账户密钥类型无效")
	}
	if saved.Email != r.Email || saved.Directory != directory {
		return nil, errors.New("ACME 账户配置不匹配")
	}
	client := &acme.Client{Key: key, DirectoryURL: directory, HTTPClient: a.client, UserAgent: "v2way/0.1", KID: acme.KeyID(saved.URI)}
	if saved.URI == "" {
		registration, err := client.Register(ctx, &acme.Account{Contact: []string{"mailto:" + r.Email}}, acme.AcceptTOS)
		if err != nil {
			return nil, err
		}
		saved.URI = registration.URI
		if err = a.manager.writeSecret("accounts", id, saved); err != nil {
			return nil, err
		}
	}
	return client, nil
}
func (a *Automation) obtain(ctx context.Context, q queuedJob) error {
	r := q.Request
	client, err := a.account(ctx, r)
	if err != nil {
		return err
	}
	var provider challenge.Provider
	if r.Challenge == "dns-01" {
		provider, err = a.provider(r.DNSProvider, r.Credentials)
		if err != nil {
			return err
		}
	}
	var attempt issuanceAttempt
	if err = a.manager.readSecret("attempts", q.Job.ID, &attempt); errors.Is(err, os.ErrNotExist) {
		key, e := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
		if e != nil {
			return e
		}
		attempt.PrivateKey, e = x509.MarshalPKCS8PrivateKey(key)
		if e != nil {
			return e
		}
	} else if err != nil {
		return err
	}
	parsed, err := x509.ParsePKCS8PrivateKey(attempt.PrivateKey)
	if err != nil {
		return errors.New("订单私钥损坏")
	}
	key, ok := parsed.(*ecdsa.PrivateKey)
	if !ok {
		return errors.New("订单私钥格式不支持")
	}
	if p, ok := provider.(*ownedProvider); ok {
		if attempt.Records != nil {
			p.records = attempt.Records
		}
		p.persist = func(records map[string]ownedRecord) error {
			attempt.Records = records
			return a.manager.writeSecret("attempts", q.Job.ID, attempt)
		}
	}
	var order *acme.Order
	if attempt.OrderURL != "" {
		a.progress(q.Job.ID, "查询上次订单状态，避免重复签发")
		order, err = client.GetOrder(ctx, attempt.OrderURL)
		if err != nil {
			return &uncertainIssue{err}
		}
		if order.Status == acme.StatusInvalid {
			order = nil
			attempt.OrderURL = ""
		}
	}
	if order == nil {
		if err = a.manager.writeSecret("attempts", q.Job.ID, attempt); err != nil {
			return err
		}
		order, err = client.AuthorizeOrder(ctx, acme.DomainIDs(r.Domains...))
		if err != nil {
			return err
		}
		attempt.OrderURL = order.URI
		if err = a.manager.writeSecret("attempts", q.Job.ID, attempt); err != nil {
			return &uncertainIssue{err}
		}
	}
	if p, ok := provider.(*ownedProvider); ok && len(p.records) > 0 {
		for token, record := range p.records {
			if err = p.remove(record); err != nil {
				return fmt.Errorf("恢复订单时清理旧 TXT 失败：%w", err)
			}
			delete(p.records, token)
		}
		if err = p.persist(p.records); err != nil {
			return err
		}
	}
	var ders [][]byte
	if order.Status == acme.StatusValid {
		ders, err = client.FetchCert(ctx, order.CertURL, true)
		if err != nil {
			return &uncertainIssue{err}
		}
	} else {
		for _, authURL := range order.AuthzURLs {
			auth, err := client.GetAuthorization(ctx, authURL)
			if err != nil {
				return err
			}
			if auth.Status == acme.StatusValid {
				continue
			}
			var selected *acme.Challenge
			for _, candidate := range auth.Challenges {
				if candidate.Type == r.Challenge {
					selected = candidate
					break
				}
			}
			if selected == nil {
				return errors.New("CA 未提供所选验证方式")
			}
			domain := auth.Identifier.Value
			// ACME wildcard authorizations contain the base identifier. DNS
			// providers expect example.com, not *.example.com, for the TXT name.
			domain = strings.TrimPrefix(domain, "*.")
			keyAuth, err := client.HTTP01ChallengeResponse(selected.Token)
			if err != nil {
				return err
			}
			if err = a.solve(ctx, q.Job.ID, client, provider, domain, keyAuth, selected, authURL); err != nil {
				return err
			}
		}
		a.progress(q.Job.ID, "域名验证完成，等待 CA 签发")
		order, err = client.WaitOrder(ctx, order.URI)
		if err != nil {
			return err
		}
		csr, err := x509.CreateCertificateRequest(rand.Reader, &x509.CertificateRequest{DNSNames: r.Domains}, key)
		if err != nil {
			return err
		}
		if order.Status == acme.StatusValid {
			ders, err = client.FetchCert(ctx, order.CertURL, true)
		} else {
			ders, _, err = client.CreateOrderCert(ctx, order.FinalizeURL, csr, true)
		}
		if err != nil {
			var caErr *acme.Error
			var orderErr *acme.OrderError
			if errors.As(err, &caErr) || errors.As(err, &orderErr) {
				return err
			}
			return &uncertainIssue{err}
		}
	}
	cert := []byte{}
	for _, der := range ders {
		cert = append(cert, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})...)
	}
	private, err := x509.MarshalPKCS8PrivateKey(key)
	if err != nil {
		return err
	}
	// Verify the returned certificate against every requested identifier before committing.
	if len(ders) == 0 {
		return errors.New("CA 未返回证书")
	}
	leaf, err := x509.ParseCertificate(ders[0])
	if err != nil {
		return err
	}
	for _, domain := range r.Domains {
		test := domain
		if strings.HasPrefix(domain, "*.") {
			test = "v2way-check." + domain[2:]
			found := false
			for _, san := range leaf.DNSNames {
				if san == domain {
					found = true
				}
			}
			if !found {
				return errors.New("CA 返回证书缺少通配符 SAN")
			}
		}
		if err = leaf.VerifyHostname(test); err != nil {
			return errors.New("CA 返回证书与申请域名不匹配")
		}
	}
	_, err = a.manager.save(q.Job.CertificateID, r.Name, string(cert), string(pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: private})), "acme", r.Environment, r.AutoRenew)
	return err
}
func (a *Automation) solve(ctx context.Context, jobID string, client *acme.Client, provider challenge.Provider, domain, keyAuth string, c *acme.Challenge, authURL string) (returnErr error) {
	if provider == nil {
		a.progress(jobID, "等待 HTTP-01 验证：公网 80 端口需能访问网关")
		key := domain + "\n" + c.Token
		a.mu.Lock()
		a.tokens[key] = httpToken{keyAuth, time.Now().Add(15 * time.Minute)}
		a.mu.Unlock()
		defer func() { a.mu.Lock(); delete(a.tokens, key); a.mu.Unlock() }()
	} else {
		a.progress(jobID, "创建 DNS TXT 验证记录")
		// CleanUp is attempted even after an ambiguous provider failure. Providers
		// remove only the matching TXT value/record, never unrelated records.
		defer func() {
			if err := provider.CleanUp(domain, c.Token, keyAuth); err != nil {
				if returnErr == nil {
					returnErr = &uncertainIssue{fmt.Errorf("DNS 清理失败：%w", err)}
				} else {
					returnErr = &uncertainIssue{fmt.Errorf("%w；DNS 清理也失败：%v", returnErr, err)}
				}
			}
		}()
		if err := provider.Present(domain, c.Token, keyAuth); err != nil {
			return err
		}
		a.progress(jobID, "等待 DNS TXT 记录传播")
		dnsCtx, cancel := context.WithTimeout(ctx, 10*time.Minute)
		defer cancel()
		if err := a.waitDNS(dnsCtx, domain, keyAuth); err != nil {
			return err
		}
	}
	if _, err := client.Accept(ctx, c); err != nil {
		return err
	}
	_, err := client.WaitAuthorization(ctx, authURL)
	return err
}
func (m *Manager) HTTPChallenge(w http.ResponseWriter, r *http.Request) bool {
	const prefix = "/.well-known/acme-challenge/"
	if !strings.HasPrefix(r.URL.Path, prefix) {
		return false
	}
	if r.TLS != nil || r.Method != "GET" && r.Method != "HEAD" {
		http.NotFound(w, r)
		return true
	}
	domain := r.Host
	if h, _, err := net.SplitHostPort(domain); err == nil {
		domain = h
	}
	domain, err := config.Domain(domain)
	token := strings.TrimPrefix(r.URL.Path, prefix)
	if err != nil || token == "" || strings.ContainsAny(token, "/\\%\r\n") {
		http.NotFound(w, r)
		return true
	}
	a := m.auto
	a.mu.Lock()
	value, ok := a.tokens[domain+"\n"+token]
	a.mu.Unlock()
	if !ok || time.Now().After(value.Expires) {
		http.NotFound(w, r)
		return true
	}
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	if r.Method == "GET" {
		fmt.Fprint(w, value.Response)
	}
	return true
}
func (a *Automation) scheduler() {
	defer a.wg.Done()
	ticker := time.NewTicker(time.Hour)
	defer ticker.Stop()
	a.renewDue()
	for {
		select {
		case <-a.ctx.Done():
			return
		case <-ticker.C:
			a.renewDue()
		}
	}
}
func (a *Automation) renewDue() {
	infos, err := a.manager.List()
	if err != nil {
		return
	}
	now := time.Now()
	for _, info := range infos {
		if info.Source != "acme" || !info.AutoRenew {
			continue
		}
		window := info.NotAfter.Sub(info.NotBefore) / 3
		if window <= 0 || window > 30*24*time.Hour {
			window = 30 * 24 * time.Hour
		}
		if now.Before(info.NotAfter.Add(-window)) {
			continue
		}
		a.mu.Lock()
		blocked := a.running[info.ID]
		var latest Job
		for _, j := range a.jobs {
			if j.CertificateID == info.ID && j.UpdatedAt.After(latest.UpdatedAt) {
				latest = j
			}
		}
		if now.Before(latest.NextAttempt) || latest.Status == "unknown" || latest.Status == "interrupted" {
			blocked = true
		}
		a.mu.Unlock()
		if !blocked {
			_, _ = a.Renew(info.ID)
		}
	}
}
