package e2e_monitoring_test

import (
	"testing"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

func TestE2EMonitoring(t *testing.T) {
	RegisterFailHandler(Fail)
	RunSpecs(t, "ROSA Regional Platform API Monitoring E2E Suite")
}
