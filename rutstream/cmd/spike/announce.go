package main

import (
	"bytes"
	"crypto/rand"
	"encoding/binary"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/anacrolix/torrent/bencode"
)

// announceResult — разобранный ответ трекера.
type announceResult struct {
	Status      int
	Body        []byte
	ContentType string
	IsBencoded  bool
	Interval    int
	Peers       int
	Failure     string
}

// announce выполняет announce-запрос к трекеру.
func announce(announceURL string, timeout time.Duration) (*announceResult, error) {
	// Случайный info_hash (20 байт) — нам не нужен реальный, достаточно
	// проверить, что трекер отвечает корректным bencode.
	ih := make([]byte, 20)
	if _, err := rand.Read(ih); err != nil {
		return nil, err
	}
	peerID := make([]byte, 20)
	copy(peerID, []byte("-RS1000-"))
	if _, err := rand.Read(peerID[8:]); err != nil {
		return nil, err
	}

	q := url.Values{}
	q.Set("info_hash", string(ih))
	q.Set("peer_id", string(peerID))
	q.Set("port", "51413")
	q.Set("uploaded", "0")
	q.Set("downloaded", "0")
	q.Set("left", "100")
	q.Set("compact", "1")
	q.Set("numwant", "50")
	q.Set("event", "started")
	q.Set("supportcrypto", "1")

	full := announceURL
	if strings.Contains(full, "?") {
		full += "&" + q.Encode()
	} else {
		full += "?" + q.Encode()
	}

	client := &http.Client{
		Timeout: timeout,
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			return http.ErrUseLastResponse
		},
	}
	req, err := http.NewRequest(http.MethodGet, full, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", "qBittorrent v4.6.0")
	req.Header.Set("Accept", "*/*")
	req.Header.Set("Connection", "close")

	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("соединение с трекером: %w", err)
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return nil, err
	}
	res := &announceResult{
		Status:      resp.StatusCode,
		Body:        body,
		ContentType: resp.Header.Get("Content-Type"),
	}
	res.parse()
	return res, nil
}

func (r *announceResult) parse() {
	if len(r.Body) == 0 {
		return
	}
	var d struct {
		Interval    int64  `bencode:"interval"`
		MinInterval int64  `bencode:"min interval"`
		Peers       string `bencode:"peers"`
		Peers6      string `bencode:"peers6"`
		Failure     string `bencode:"failure reason"`
	}
	if err := bencode.Unmarshal(r.Body, &d); err != nil {
		return
	}
	r.IsBencoded = true
	r.Interval = int(d.Interval)
	r.Failure = d.Failure
	n := 0
	if len(d.Peers) > 0 {
		n += len(d.Peers) / 6
	}
	if len(d.Peers6) > 0 {
		n += len(d.Peers6) / 18
	}
	r.Peers = n
}

// announceURLs — список announce-серверов rutracker, которые встречаются в магнитах.
var announceURLs = []string{
	"http://bt2.t-ru.org/ann",
	"http://bt3.t-ru.org/ann",
}

func bytesReader(b []byte) io.Reader { return bytes.NewReader(b) }

// peerAddrFromCompact разбирает компактный список пиров.
func peerAddrFromCompact(s string) []string {
	var out []string
	for i := 0; i+6 <= len(s); i += 6 {
		ip := net.IP(s[i : i+4]).String()
		port := binary.BigEndian.Uint16([]byte(s[i+4 : i+6]))
		out = append(out, net.JoinHostPort(ip, strconv.Itoa(int(port))))
	}
	return out
}
