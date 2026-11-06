package gateway

import (
	"io"
	"sync/atomic"
)

// UpstreamStats counts business proxy requests and processed body/tunnel bytes.
// Header, TLS, and health-probe traffic are excluded.
type UpstreamStats struct {
	Requests      uint64 `json:"requests"`
	UploadBytes   uint64 `json:"uploadBytes"`
	DownloadBytes uint64 `json:"downloadBytes"`
}
type upstreamCounters struct {
	requests atomic.Uint64
	upload   atomic.Uint64
	download atomic.Uint64
}

func (e *Engine) UpstreamStats() map[string]UpstreamStats {
	snapshot := e.current.Load()
	result := make(map[string]UpstreamStats, len(snapshot.pools))
	for id, pool := range snapshot.pools {
		result[id] = UpstreamStats{pool.counters.requests.Load(), pool.counters.upload.Load(), pool.counters.download.Load()}
	}
	return result
}

type meteredBody struct {
	io.ReadCloser
	counter *atomic.Uint64
}

func (body *meteredBody) Read(buffer []byte) (int, error) {
	n, err := body.ReadCloser.Read(buffer)
	body.counter.Add(uint64(n))
	return n, err
}

type meteredDuplex struct {
	io.ReadWriteCloser
	counters *upstreamCounters
}

func (body *meteredDuplex) Read(buffer []byte) (int, error) {
	n, err := body.ReadWriteCloser.Read(buffer)
	body.counters.download.Add(uint64(n))
	return n, err
}
func (body *meteredDuplex) Write(buffer []byte) (int, error) {
	n, err := body.ReadWriteCloser.Write(buffer)
	body.counters.upload.Add(uint64(n))
	return n, err
}

type closeWriter interface{ CloseWrite() error }
type meteredHalfCloseDuplex struct {
	*meteredDuplex
	writer closeWriter
}

func (body *meteredHalfCloseDuplex) CloseWrite() error { return body.writer.CloseWrite() }
func meterResponseBody(body io.ReadCloser, counters *upstreamCounters) io.ReadCloser {
	if duplex, ok := body.(io.ReadWriteCloser); ok {
		metered := &meteredDuplex{duplex, counters}
		if half, ok := duplex.(closeWriter); ok {
			return &meteredHalfCloseDuplex{metered, half}
		}
		return metered
	}
	return &meteredBody{body, &counters.download}
}
