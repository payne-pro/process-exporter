package main

import (
	//	"bytes"
	//	"encoding/json"
	"flag"
	"fmt"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promhttp"
	"github.com/prometheus/common/version"
	"github.com/shirou/gopsutil/mem"
	"github.com/shirou/gopsutil/process"
	"github.com/sirupsen/logrus"
	"net"
	"regexp"

	//	"io/ioutil"
	"net/http"
	//	"net/url"
	"os"
	//	"regexp"
	binrpc "github.com/florentchauveau/go-kamailio-binrpc/v3"
	"strconv"
	"strings"
)

// MetricValue is the value of a metric, with its labels.
type MetricValue struct {
	Value  float64
	Labels map[string]string
}

const (
	namespace = "indivdual"
)

var (
	to_watch = []string{}
	to_skip  = []string{}

	codeRegex = regexp.MustCompile("^[0-9x]{3}$")
)

type Exporter struct {
	up             prometheus.Gauge
	totalScrapes   prometheus.Counter
	processMetrics map[string]*prometheus.GaugeVec

	conn net.Conn
}

// NewExporter returns an initialized Exporter.
func NewExporter(processMetrics map[string]*prometheus.GaugeVec) (*Exporter, error) {

	return &Exporter{
		up: prometheus.NewGauge(prometheus.GaugeOpts{
			Namespace: namespace,
			Name:      "up",
			Help:      "Was the last scrape of processes successful.",
		}),
		totalScrapes: prometheus.NewCounter(prometheus.CounterOpts{
			Namespace: namespace,
			Name:      "exporter_total_scrapes",
			Help:      "Current total process scrapes.",
		}),
		processMetrics: processMetrics,
	}, nil
}

func (e *Exporter) scrape() {
	e.totalScrapes.Inc()
	e.up.Set(0)

	procs, err := process.Pids()
	if err != nil {
		logrus.Errorf("Couldn't retrieve processes: %v", err)
		return
	}

	for _, pid := range procs {
		proc, err := process.NewProcess(pid)
		if err == nil {
			cmd_line, _ := proc.Cmdline()
			for _, proc_arg := range to_watch {
				want_process := false
				if strings.Contains(cmd_line, proc_arg) {
					want_process = true
					for _, skip_arg := range to_skip {
						if strings.Contains(cmd_line, skip_arg) {
							want_process = false
						}
					}
				}
				if want_process {
					e.processMetrics[proc_arg+strconv.Itoa(int(pid))] = prometheus.NewGaugeVec(
						prometheus.GaugeOpts{
							Namespace: namespace,
							Name:      "process",
							Help:      "metrics of the process",
						},
						[]string{
							"process",
							"pid",
							"metric",
						},
					)
					// See http://godoc.org/github.com/shirou/gopsutil/process
					// {"rss":2514944,"vms":110858240,"shared":2113536,"text":897024,"lib":0,"data":0,"dirty":36003840}
					// {"cpu":"cpu","user":0.0,"system":0.0,"idle":0.0,"nice":0.0,"iowait":0.0,"irq":0.0,"softirq":0.0,"steal":0.0,"guest":0.0,"guestNice":0.0,"stolen":0.0}

					meminfo, _ := mem.VirtualMemory()
					e.processMetrics[proc_arg+strconv.Itoa(int(pid))].With(prometheus.Labels{"process": proc_arg, "pid": strconv.Itoa(int(pid)), "metric": "memory_shared"}).Set(float64(meminfo.Shared))
					e.processMetrics[proc_arg+strconv.Itoa(int(pid))].With(prometheus.Labels{"process": proc_arg, "pid": strconv.Itoa(int(pid)), "metric": "memory_dirty"}).Set(float64(meminfo.Dirty))
					e.processMetrics[proc_arg+strconv.Itoa(int(pid))].With(prometheus.Labels{"process": proc_arg, "pid": strconv.Itoa(int(pid)), "metric": "memory_percent"}).Set(float64(meminfo.UsedPercent))
					e.processMetrics[proc_arg+strconv.Itoa(int(pid))].With(prometheus.Labels{"process": proc_arg, "pid": strconv.Itoa(int(pid)), "metric": "memory_total"}).Set(float64(meminfo.Total))

					cpuinfo, _ := proc.Times()
					cpofds, _ := proc.OpenFiles()

					e.processMetrics[proc_arg+strconv.Itoa(int(pid))].With(prometheus.Labels{"process": proc_arg, "pid": strconv.Itoa(int(pid)), "metric": "cpu_user"}).Set(cpuinfo.User)
					e.processMetrics[proc_arg+strconv.Itoa(int(pid))].With(prometheus.Labels{"process": proc_arg, "pid": strconv.Itoa(int(pid)), "metric": "cpu_system"}).Set(cpuinfo.System)
					e.processMetrics[proc_arg+strconv.Itoa(int(pid))].With(prometheus.Labels{"process": proc_arg, "pid": strconv.Itoa(int(pid)), "metric": "cpu_idle"}).Set(cpuinfo.Idle)
					//e.processMetrics[proc_arg+strconv.Itoa(int(pid))].With(prometheus.Labels{"process": proc_arg, "pid": strconv.Itoa(int(pid)), "metric": "proc_fds"}).Set(float64(cpofds))

					//if len(cpofds) > 0 {
					//	fmt.Printf("myslice1 = %v\n", cpofds[].Fd)
					//}
					res := 0
					for index := range cpofds {
						res += int(cpofds[index].Fd)
					}

					fmt.Printf("myslice[%v] = %v\n", pid, res)

					//fmt.Printf("myslice1 = %v\n", cpofds)

				}
			}
		}
	}

	e.up.Set(1)
}

func (e *Exporter) resetMetrics() {
	for _, m := range e.processMetrics {
		m.Reset()
	}
}

func (e *Exporter) collectMetrics(metrics chan<- prometheus.Metric) {
	for _, m := range e.processMetrics {
		m.Collect(metrics)
	}
}

// Describe describes all the metrics ever exported by the HAProxy exporter. It
// implements prometheus.Collector.
func (e *Exporter) Describe(ch chan<- *prometheus.Desc) {
	ch <- e.up.Desc()
	ch <- e.totalScrapes.Desc()
}

// Collect fetches the stats from configured HAProxy location and delivers them
// as Prometheus metrics. It implements prometheus.Collector.
func (e *Exporter) Collect(ch chan<- prometheus.Metric) {
	e.resetMetrics()
	e.scrape()

	ch <- e.up
	ch <- e.totalScrapes
	e.collectMetrics(ch)
}

// scrapeMethod will return metrics for one method.
func (e *Exporter) scrapeMethod(method string) (map[string][]MetricValue, error) {
	records, err := e.fetchBINRPC(method)

	if err != nil {
		return nil, err
	}

	// we expect just 1 record of type map
	if len(records) == 2 && records[0].Type == binrpc.TypeInt && records[0].Value.(int) == 500 {
		return nil, fmt.Errorf(`invalid response for method "%s": [500] %s`, method, records[1].Value.(string))
	} else if len(records) != 1 {
		return nil, fmt.Errorf(`invalid response for method "%s", expected %d record, got %d`,
			method, 1, len(records),
		)
	}

	// all methods implemented in this exporter return a struct
	items, err := records[0].StructItems()

	if err != nil {
		return nil, err
	}

	metrics := make(map[string][]MetricValue)

	switch method {
	case "sl.stats":
		fallthrough
	case "tm.stats":
		for _, item := range items {
			i, _ := item.Value.Int()

			if codeRegex.MatchString(item.Key) {
				// this item is a "code" statistic, eg "200" or "6xx"
				metrics["codes"] = append(metrics["codes"],
					MetricValue{
						Value: float64(i),
						Labels: map[string]string{
							"code": item.Key,
						},
					},
				)
			} else {
				metrics[item.Key] = []MetricValue{{Value: float64(i)}}
			}
		}
	case "tls.info":
		fallthrough
	case "core.shmmem":
		fallthrough
	case "core.tcp_info":
		fallthrough
	case "dlg.stats_active":
		fallthrough
	case "core.uptime":
		for _, item := range items {
			i, _ := item.Value.Int()
			metrics[item.Key] = []MetricValue{{Value: float64(i)}}
		}
	}

	return metrics, nil
}

// fetchBINRPC talks to kamailio using the BINRPC protocol.
func (e *Exporter) fetchBINRPC(method string) ([]binrpc.Record, error) {
	// WritePacket returns the cookie generated
	cookie, err := binrpc.WritePacket(e.conn, "core.uptime")

	if err != nil {
		return nil, err
	}

	// the cookie is passed again for verification
	// we receive records in response
	records, err := binrpc.ReadPacket(e.conn, cookie)

	if err != nil {
		return nil, err
	}

	return records, nil
}

// Main function
func main() {
	const processHelpText = `Processes to (no)watch
	You should provided at least one process to watch.
	The parameter process.watch should be a comma-seperated list of regular expressions of processes to watch
	The parameter process.nowatch is a filter that removes processes from the list provided by process.watch`

	var (
		listenAddress   = flag.String("web.listen-address", ":8980", "Address to listen on for web interface and telemetry.")
		metricsPath     = flag.String("web.telemetry-path", "/metrics", "Path under which to expose metrics.")
		showVersion     = flag.Bool("version", false, "Print version information.")
		processExpr     = flag.String("process.watch", "", processHelpText)
		processExprSkip = flag.String("process.nowatch", "", processHelpText)
	)
	flag.Parse()

	if *showVersion {
		fmt.Fprintln(os.Stdout, version.Print("process_exporter"))
		os.Exit(0)
	}

	logrus.Infoln("Starting process_exporter", version.Info())
	logrus.Infoln("Build context", version.BuildContext())

	// Don't split empty strings, it gives non-empty arrays?
	if *processExpr != "" {
		logrus.Infoln("Watch:", *processExpr)
		to_watch = strings.Split(*processExpr, ",")
	} else {
		logrus.Infoln("Empty list to watch")
		to_watch = []string{}
	}
	if *processExprSkip != "" {
		logrus.Infoln("Skip:", *processExprSkip)
		to_skip = strings.Split(*processExprSkip, ",")
	} else {
		logrus.Infoln("Empty list to skip")
		to_skip = []string{}
	}

	metrics := map[string]*prometheus.GaugeVec{}
	exporter, err := NewExporter(metrics)
	if err != nil {
		logrus.Fatal(err)
	}

	prometheus.MustRegister(exporter)
	prometheus.MustRegister(version.NewCollector("process_exporter"))

	logrus.Infoln("Listening on", *listenAddress)
	http.Handle(*metricsPath, promhttp.Handler())
	http.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`<html>
		<head><title>Process Exporter</title></head>
		<body>
		<h1>Process Exporter</h1>
		<p><a href='` + *metricsPath + `'>Metrics</a></p>
		</body>
		</html>`))
	})
	logrus.Fatal(http.ListenAndServe(*listenAddress, nil))

}
