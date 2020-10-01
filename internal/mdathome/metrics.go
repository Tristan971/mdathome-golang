package mdathome

import (
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"
)

type RequestCounter struct {
	elapsed prometheus.Counter
	count   prometheus.Counter
}

const RequestHit = "HIT"
const RequestMiss = "MISS"

var counters map[string]RequestCounter

func initMetrics() {
	for _, requestType := range [3]string{RequestHit, RequestMiss} {
		counters[requestType] = RequestCounter{
			elapsed: promauto.NewCounter(prometheus.CounterOpts{
				Name: "request_elapsed",
				ConstLabels: prometheus.Labels{
					"Type": requestType,
				},
			}),
			count: promauto.NewCounter(prometheus.CounterOpts{
				Name: "request_total_count",
				ConstLabels: prometheus.Labels{
					"Type": requestType,
				},
			}),
		}
	}
}

func recordRequest(hit bool, elapsed float64) {
	var requestType = RequestMiss
	if hit {
		requestType = RequestHit
	}

	var counter = counters[requestType]
	counter.count.Inc()
	counter.elapsed.Add(elapsed)
}
