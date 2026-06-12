package messages_search

import (
	"context"
	"sync"
	"time"

	"github.com/olivere/elastic"
)

var (
	osMu     sync.Mutex
	osClient *elastic.Client
)

// ESClient returns the process-wide olivere/elastic v6 client connected to the
// configured OpenSearch read cluster. Sniffing is disabled because the cluster
// usually sits behind a service VIP that does not expose intra-cluster IPs to
// callers.
//
// Self-healing: a previous sync.Once layout meant a single ping failure at
// boot would pin osErr forever; we now re-attempt construction on every call
// that finds the cached client nil. Successful builds are cached; failed
// builds are retried on the next request rather than poisoning the cache.
func ESClient(cfg SearchConfig) (*elastic.Client, error) {
	osMu.Lock()
	defer osMu.Unlock()
	if osClient != nil {
		return osClient, nil
	}
	c, err := buildESClient(cfg)
	if err != nil {
		return nil, err
	}
	osClient = c
	return osClient, nil
}

func buildESClient(cfg SearchConfig) (*elastic.Client, error) {
	opts := []elastic.ClientOptionFunc{
		elastic.SetURL(cfg.OSAddrs...),
		elastic.SetSniff(false),
		elastic.SetHealthcheck(true),
		elastic.SetHealthcheckTimeout(3 * time.Second),
	}
	if cfg.OSUsername != "" {
		opts = append(opts, elastic.SetBasicAuth(cfg.OSUsername, cfg.OSPassword))
	}
	c, err := elastic.NewClient(opts...)
	if err != nil {
		return nil, err
	}
	if len(cfg.OSAddrs) > 0 {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if _, _, err := c.Ping(cfg.OSAddrs[0]).Do(ctx); err != nil {
			return nil, err
		}
	}
	return c, nil
}

// resetESClientForTest is only called from tests to swap in fakes.
func resetESClientForTest() {
	osMu.Lock()
	defer osMu.Unlock()
	osClient = nil
}
