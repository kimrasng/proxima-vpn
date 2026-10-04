package services

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/proximavpn/proxima-vpn/pkg/models"
)

var reconcileNow = time.Date(2026, 9, 30, 12, 0, 0, 0, time.UTC)

type reconcileProvider struct {
	mu                 sync.Mutex
	records            map[string]CloudflareDNSRecord
	next               int
	faultMethod        string
	faultStatus        int
	lost               bool
	failReadAfterWrite bool
	retryAfter         string
	started            chan struct{}
	resume             chan struct{}
	requests           int
}

func (p *reconcileProvider) serve(w http.ResponseWriter, r *http.Request) {
	p.mu.Lock()
	p.requests++
	method, status, lost := p.faultMethod, p.faultStatus, p.lost
	if method == r.Method {
		p.faultMethod = ""
	}
	p.mu.Unlock()
	if method == r.Method && !lost {
		w.Header().Set("Retry-After", p.retryAfter)
		w.WriteHeader(status)
		fmt.Fprint(w, `{"success":false,"errors":[{"code":1000}]}`)
		return
	}
	if r.Method == http.MethodPost && p.started != nil {
		p.started <- struct{}{}
		<-p.resume
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	var result any
	switch r.Method {
	case http.MethodGet:
		records := []CloudflareDNSRecord{}
		for _, record := range p.records {
			if record.Name == r.URL.Query().Get("name.exact") && (r.URL.Query().Get("type") == "" || r.URL.Query().Get("type") == record.Type) {
				records = append(records, record)
			}
		}
		result = records
		fmt.Fprintf(w, `{"success":true,"result":%s,"result_info":{"page":1,"total_pages":1}}`, jsonValue(records))
		return
	case http.MethodPost, http.MethodPut:
		var record CloudflareDNSRecord
		if err := json.NewDecoder(r.Body).Decode(&record); err != nil {
			panic(err)
		}
		if r.Method == http.MethodPost {
			p.next++
			record.ID = "id-" + strconv.Itoa(p.next)
		} else {
			record.ID = r.URL.Path[strings.LastIndex(r.URL.Path, "/")+1:]
		}
		p.records[record.ID] = record
		result = record
	case http.MethodDelete:
		id := r.URL.Path[strings.LastIndex(r.URL.Path, "/")+1:]
		delete(p.records, id)
		result = map[string]string{"id": id}
	}
	if method == r.Method && lost {
		if p.failReadAfterWrite {
			p.faultMethod, p.faultStatus, p.lost = http.MethodGet, 503, false
		}
		w.WriteHeader(status)
		fmt.Fprint(w, `{"success":false,"errors":[{"code":1000}]}`)
		return
	}
	fmt.Fprintf(w, `{"success":true,"result":%s}`, jsonValue(result))
}

func jsonValue(v any) string {
	b, err := json.Marshal(v)
	if err != nil {
		panic(err)
	}
	return string(b)
}

type reconcileFixture struct {
	pool       *pgxpool.Pool
	owner      string
	provider   *reconcileProvider
	reconciler *ManagedEntryDNSReconciler
	server     *httptest.Server
}

func newReconcileFixture(t *testing.T) *reconcileFixture {
	t.Helper()
	pool := phs028ServiceDB(t)
	owner := entryNode(t, pool, "relay")
	ensureEntry(t, pool, owner, entryConfig)
	p := &reconcileProvider{records: make(map[string]CloudflareDNSRecord)}
	srv := httptest.NewServer(http.HandlerFunc(p.serve))
	t.Cleanup(srv.Close)
	client := NewCloudflareDNSClient("secret")
	client.baseURL, client.httpClient = srv.URL, srv.Client()
	f := &reconcileFixture{pool: pool, owner: owner, provider: p, server: srv}
	f.reconciler = NewManagedEntryDNSReconciler(ManagedEntryDNSReconcileDependencies{
		Store: NewManagedEntryDNSWorkerStore(pool), Client: client, Intent: entryConfig,
		Now: func() time.Time { return reconcileNow }, Jitter: func() float64 { return 0 },
	})
	return f
}

func (f *reconcileFixture) seed(t *testing.T, typ, marker, content string) string {
	t.Helper()
	s := readEntry(t, f.pool, f.owner)
	f.provider.mu.Lock()
	defer f.provider.mu.Unlock()
	f.provider.next++
	id := "id-" + strconv.Itoa(f.provider.next)
	f.provider.records[id] = CloudflareDNSRecord{ID: id, Name: s.hostname, Type: typ, Comment: marker, Content: content, TTL: 300}
	return id
}

func (f *reconcileFixture) records() []CloudflareDNSRecord {
	f.provider.mu.Lock()
	defer f.provider.mu.Unlock()
	var records []CloudflareDNSRecord
	for _, r := range f.provider.records {
		records = append(records, r)
	}
	return records
}

func (f *reconcileFixture) state(t *testing.T) entryState { return readEntry(t, f.pool, f.owner) }

func (f *reconcileFixture) setDelete(t *testing.T) {
	t.Helper()
	if _, err := f.pool.Exec(t.Context(), `UPDATE managed_entry_dns SET desired_action='delete',dns_status='deleting',generation=generation+1,cleanup_requested_at=NOW(),next_attempt_at=NOW() WHERE owner_node_id=$1`, f.owner); err != nil {
		t.Fatal(err)
	}
}

func assertReconcileState(t *testing.T, f *reconcileFixture, status models.ManagedDNSStatus, code models.ManagedDNSErrorCode, count int) {
	t.Helper()
	s := f.state(t)
	if s.status != string(status) || (code == "" && s.errorCode != nil) || (code != "" && (s.errorCode == nil || *s.errorCode != string(code))) || len(f.records()) != count {
		t.Fatalf("state=%+v records=%+v", s, f.records())
	}
}
