package main

// アプリ内 DNS サーバー（PowerDNS の置き換え）。
//
// ベンチは配信者ごとのサブドメイン <name>.u.isucon.local を名前解決してから HTTPS でアクセスし、
// さらに DNS 水責め攻撃（ランダムなサブドメイン）を行う。PowerDNS は gmysql バックエンドで
// キャッシュ無効だったため、1問い合わせごとに MySQL を 2〜3 回叩いていた（DB 時間の 36%、pdns CPU 29%）。
//
// 応答内容は PowerDNS のときと同じ:
//   - ゾーンファイル（webapp/pdns/u.isucon.local.zone）にある名前と、登録済みユーザー名 → A <入口ノードのIP>
//   - それ以外 → NXDOMAIN
// ユーザー登録は users キャッシュに入った時点で解決できる（DB コミット後）。
// initialize / 起動時にユーザーキャッシュを DB から作り直すので、再起動しても状態は DB から復元される。

import (
	"bufio"
	"fmt"
	"hash/fnv"
	"log"
	"net"
	"os"
	"strings"
	"sync"

	"github.com/miekg/dns"
)

const (
	dnsZone     = "u.isucon.local."
	dnsTTL      = 120 // ベンチは TTL の間だけ結果をキャッシュする（マニュアル）。長いほど問い合わせが減る
	dnsZoneFile = "../pdns/u.isucon.local.zone"
)

var (
	dnsStaticNames = map[string]struct{}{} // ゾーンファイル由来の名前（apex は ""）
	dnsAddr        net.IP                  // ゾーンファイル由来の名前（pipe など）に返す IP
	dnsUserAddrs   []net.IP                // 配信者サブドメイン（登録ユーザー名）に返す IP（名前のハッシュで選ぶ）。空なら dnsAddr
	dnsSOA         *dns.SOA
	dnsMu          sync.RWMutex
)

// ゾーンファイルから静的な名前を読む（"<name> 0 IN A <ISUCON_SUBDOMAIN_ADDRESS>" の行）
func loadDNSZone(path string) error {
	f, err := os.Open(path)
	if err != nil {
		return err
	}
	defer f.Close()
	names := map[string]struct{}{}
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		fs := strings.Fields(sc.Text())
		if len(fs) >= 5 && fs[2] == "IN" && fs[3] == "A" {
			n := strings.ToLower(fs[0])
			if n == "@" {
				n = ""
			}
			names[n] = struct{}{}
		}
	}
	if err := sc.Err(); err != nil {
		return err
	}
	dnsMu.Lock()
	dnsStaticNames = names
	dnsMu.Unlock()
	return nil
}

func dnsNameExists(sub string) bool {
	dnsMu.RLock()
	_, ok := dnsStaticNames[sub]
	dnsMu.RUnlock()
	if ok {
		return true
	}
	if sub == "" {
		return false
	}
	return users.hasLowerName(sub)
}

func handleDNS(w dns.ResponseWriter, r *dns.Msg) {
	m := new(dns.Msg)
	m.SetReply(r)
	m.Authoritative = true
	m.RecursionAvailable = false

	if len(r.Question) == 0 {
		w.WriteMsg(m)
		return
	}
	q := r.Question[0]
	qname := strings.ToLower(q.Name)

	// ゾーン外
	if qname != dnsZone && !strings.HasSuffix(qname, "."+dnsZone) {
		m.Rcode = dns.RcodeRefused
		w.WriteMsg(m)
		return
	}
	sub := strings.TrimSuffix(strings.TrimSuffix(qname, dnsZone), ".")

	if !dnsNameExists(sub) {
		m.Rcode = dns.RcodeNameError
		m.Ns = []dns.RR{dnsSOA}
		w.WriteMsg(m)
		return
	}

	switch q.Qtype {
	case dns.TypeA, dns.TypeANY:
		// 配信者サブドメイン（ユーザー名。初期データのユーザーも含む）は名前のハッシュで dnsUserAddrs から選び、
		// それ以外（pipe や www などゾーンファイルの特別な名前、apex）は dnsAddr
		addr := dnsAddr
		if len(dnsUserAddrs) > 0 && sub != "" && users.hasLowerName(sub) {
			h := fnv.New32a()
			h.Write([]byte(sub))
			addr = dnsUserAddrs[int(h.Sum32()%uint32(len(dnsUserAddrs)))]
		}
		m.Answer = []dns.RR{&dns.A{
			Hdr: dns.RR_Header{Name: q.Name, Rrtype: dns.TypeA, Class: dns.ClassINET, Ttl: dnsTTL},
			A:   addr,
		}}
	case dns.TypeSOA:
		if sub == "" {
			m.Answer = []dns.RR{dnsSOA}
		} else {
			m.Ns = []dns.RR{dnsSOA}
		}
	case dns.TypeNS:
		if sub == "" {
			m.Answer = []dns.RR{&dns.NS{
				Hdr: dns.RR_Header{Name: q.Name, Rrtype: dns.TypeNS, Class: dns.ClassINET, Ttl: dnsTTL},
				Ns:  "ns1." + dnsZone,
			}}
		} else {
			m.Ns = []dns.RR{dnsSOA}
		}
	default:
		// 名前はあるが型が無い: NOERROR / 空の応答
		m.Ns = []dns.RR{dnsSOA}
	}
	w.WriteMsg(m)
}

// DNS サーバーを起動する（UDP/53）。失敗したら終了する。
func startDNSServer(addr string) error {
	dnsAddr = net.ParseIP(addr).To4()
	if dnsAddr == nil {
		return fmt.Errorf("invalid DNS answer address: %q", addr)
	}
	// カンマ区切り。同じ IP を複数書けば重み付けになる（例: "ip1,ip3,ip3" なら 1:2）
	if v, ok := os.LookupEnv("ISUCON13_DNS_USER_ADDRESS"); ok && v != "" {
		for _, a := range strings.Split(v, ",") {
			ip := net.ParseIP(strings.TrimSpace(a)).To4()
			if ip == nil {
				return fmt.Errorf("invalid ISUCON13_DNS_USER_ADDRESS: %q", v)
			}
			dnsUserAddrs = append(dnsUserAddrs, ip)
		}
	}
	if err := loadDNSZone(dnsZoneFile); err != nil {
		return fmt.Errorf("load zone file: %w", err)
	}
	dnsSOA = &dns.SOA{
		Hdr:     dns.RR_Header{Name: dnsZone, Rrtype: dns.TypeSOA, Class: dns.ClassINET, Ttl: 3600},
		Ns:      "ns1." + dnsZone,
		Mbox:    "hostmaster." + dnsZone,
		Serial:  0,
		Refresh: 10800,
		Retry:   3600,
		Expire:  604800,
		Minttl:  3600,
	}
	dns.HandleFunc(".", handleDNS)
	udp := &dns.Server{Addr: ":53", Net: "udp", UDPSize: 65535}
	tcp := &dns.Server{Addr: ":53", Net: "tcp"}
	errc := make(chan error, 2)
	go func() { errc <- udp.ListenAndServe() }()
	go func() { errc <- tcp.ListenAndServe() }()
	go func() {
		if err := <-errc; err != nil {
			log.Printf("dns server exited: %v", err)
			os.Exit(1)
		}
	}()
	return nil
}
