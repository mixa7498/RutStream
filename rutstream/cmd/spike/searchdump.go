package main

import (
	"crypto/rand"
	"fmt"
	"net/url"
	"time"
)

func runSearchDump(args []string) error {
	ih := "E8D6B5775A4F1AD7DFB62F43A6034FB0E9480870"
	raw := make([]byte, 20)
	for i := range raw {
		raw[i] = 0
	}
	copy(raw, []byte(ih))
	peerID := make([]byte, 20)
	copy(peerID, []byte("-RS1000-"))
	rand.Read(peerID[8:])

	for _, ann := range []string{
		"http://bt2.t-ru.org/ann",
		"http://bt2.t-ru.org/ann?magnet",
		"http://bt2.t-ru.org/ann?0-53850092-FxAbGm6zOsGC9JL77GYK",
	} {
		q := url.Values{}
		q.Set("info_hash", string(raw))
		q.Set("peer_id", string(peerID))
		q.Set("port", "51413")
		q.Set("uploaded", "0")
		q.Set("downloaded", "0")
		q.Set("left", "100")
		q.Set("compact", "1")
		q.Set("numwant", "50")
		q.Set("event", "started")
		res, err := announce(ann+"?"+q.Encode(), 20*time.Second)
		if err != nil {
			fmt.Printf("%-60s ошибка: %v\n", ann, err)
			continue
		}
		body := res.Body
		if len(body) > 160 {
			body = body[:160]
		}
		fmt.Printf("%-60s HTTP %d bencoded=%v тело=%q\n", ann, res.Status, res.IsBencoded, string(body))
	}
	return nil
}
