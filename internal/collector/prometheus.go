package collector

import (
	promv1 "github.com/prometheus/client_golang/api/prometheus/v1"
	"github.com/prometheus/common/model"
)

type PrometheusCollector struct {
	api promv1.API
}

func NewPrometheusCollector(api promv1.API) *PrometheusCollector {
	return &PrometheusCollector{
		api: api,
	}
}
