package main

import (
	"encoding/json"
	"errors"
	"fmt"
	libvirt "github.com/libvirt/libvirt-go"
	"io/ioutil"
	"log"
	"os"
	"path"
	"text/template"
	"time"
)

// specifications and skeleton plugin:
// https://github.com/firehol/netdata/wiki/External-Plugins

const confFile string = "netdata-virt.json"

type Config struct {
	URI             string
	IntervalSeconds int
}

func (conf *Config) ReadFile(path string) error {
	content, err := ioutil.ReadFile(path)
	if err != nil {
		return err
	}

	if len(content) > 0 {
		err = json.Unmarshal(content, conf)
		if err != nil {
			return err
		}
	}
	return nil
}

type Interval struct {
	Cmdline time.Duration
	Environ time.Duration
	Config  time.Duration
}

func (intv *Interval) Pick() time.Duration {
	val := time.Duration(1) * time.Second
	if intv.Cmdline < intv.Config {
		val = intv.Config
	} else {
		val = intv.Cmdline
	}
	if val < intv.Environ {
		return intv.Environ
	}
	return val
}

func NewInterval() Interval {
	val := time.Duration(1) * time.Second
	return Interval{Cmdline: val, Environ: val, Config: val}
}

func (intv *Interval) Fill(conf Config, envVar string) error {
	if conf.IntervalSeconds <= 0 {
		return errors.New(fmt.Sprintf("invalid interval: %d", conf.IntervalSeconds))
	}

	intv.Config = time.Duration(conf.IntervalSeconds) * time.Second

	var err error
	intv.Cmdline, err = time.ParseDuration(os.Args[1])
	if err != nil {
		return err
	}
	if envVar != "" {
		intv.Environ, err = time.ParseDuration(envVar)
		if err != nil {
			return err
		}
	}

	return nil
}

func getInterval(conf Config) time.Duration {
	intv := NewInterval()
	err := intv.Fill(conf, os.Getenv("NETDATA_UPDATE_EVERY"))
	if err != nil {
		log.Fatalf("unknown duration: %s", err)
	}
	return intv.Pick()
}

type ChartTemplates struct {
	Graph     *template.Template
	DataPoint *template.Template
}

type Charts struct {
	templates  map[string]ChartTemplates
	lastUpdate time.Time
}

func NewCharts() Charts {
	return Charts{
		templates:  make(map[string]ChartTemplates),
		lastUpdate: time.Now(),
	}
}

type VMStats struct {
	ChartName string
	VmName    string
	Interval  int64
	Pcpu      *libvirt.DomainStatsCPU
	Balloon   *libvirt.DomainStatsBalloon
	Net       *libvirt.DomainStatsNet
	Block     *libvirt.DomainStatsBlock
}

func (ch *Charts) Update(now time.Time, domStats []libvirt.DomainStats) {
	var err error
	actualInterval := now.Sub(ch.lastUpdate)
	ch.lastUpdate = now

	for _, domStat := range domStats {
		vmStats := VMStats{
			ChartName: "",
			VmName:    "",
			Interval:  actualInterval.Nanoseconds() / 1000, // microseconds
			Pcpu:      domStat.Cpu,
			Balloon:   domStat.Balloon,
		}
		vmStats.VmName, err = domStat.Domain.GetName()
		if err != nil {
			log.Printf("error collecting libvirt stats for Domain <>: %s", err)
			continue
		}

		vmStats.ChartName = fmt.Sprintf("virt.vm_%s_pcpu_time", vmStats.VmName)
		ch.updateFromTemplates(&vmStats, "pcpu")

		vmStats.ChartName = fmt.Sprintf("virt.vm_%s_balloon", vmStats.VmName)
		ch.updateFromTemplates(&vmStats, "balloon")

		for _, domNetStat := range domStat.Net {
			vmStats.Net = &domNetStat
			vmStats.ChartName = fmt.Sprintf("virt.vm_%s_nic_%s_traffic", vmStats.VmName, vmStats.Net.Name)
			ch.updateFromTemplates(&vmStats, "nic_traffic")
			vmStats.ChartName = fmt.Sprintf("virt.vm_%s_nic_%s_errors", vmStats.VmName, vmStats.Net.Name)
			ch.updateFromTemplates(&vmStats, "nic_errors")
			vmStats.ChartName = fmt.Sprintf("virt.vm_%s_nic_%s_drops", vmStats.VmName, vmStats.Net.Name)
			ch.updateFromTemplates(&vmStats, "nic_drops")
		}

		for _, domBlockStat := range domStat.Block {
			vmStats.Block = &domBlockStat
			vmStats.ChartName = fmt.Sprintf("virt.vm_%s_drive_%s_traffic", vmStats.VmName, vmStats.Block.Name)
			ch.updateFromTemplates(&vmStats, "block_traffic")
			vmStats.ChartName = fmt.Sprintf("virt.vm_%s_drive_%s_iops", vmStats.VmName, vmStats.Block.Name)
			ch.updateFromTemplates(&vmStats, "block_iops")
			vmStats.ChartName = fmt.Sprintf("virt.vm_%s_drive_%s_times", vmStats.VmName, vmStats.Block.Name)
			ch.updateFromTemplates(&vmStats, "block_times")
		}
	}
}

// fields documentation:
// https://github.com/firehol/netdata/wiki/External-Plugins

var graphTemplates = map[string]string{
	"pcpu": `CHART {{.ChartName}} '' 'pcpu time spent' 'ns' 'pcpu' 'cputime' stacked
DIMENSION vm_{{.VmName}}_pcpu_time total
DIMENSION vm_{{.VmName}}_pcpu_user user
DIMENSION vm_{{.VmName}}_pcpu_sys sys
`,
	"balloon": `CHART {{.ChartName}} '' 'balloon size' 'kiB' 'balloon' 'ram' stacked
DIMENSION vm_{{.VmName}}_balloon_current current
DIMENSION vm_{{.VmName}}_balloon_maximum maximum
`,
	"nic_traffic": `CHART {{.ChartName}} '' 'NIC traffic' 'bytes' {{.Net.Name}} 'traffic' stacked
DIMENSION vm_{{.VmName}}_nic_{{.Net.Name}}_rx_bytes
DIMENSION vm_{{.VmName}}_nic_{{.Net.Name}}_tx_bytes
`,
	"nic_errors": ` CHART {{.ChartName}} '' 'NIC errors count' 'count' {{.Net.Name}} 'count' stacked
DIMENSION vm_{{.VmName}}_nic_{{.Net.Name}}_rx_errs
DIMENSION vm_{{.VmName}}_nic_{{.Net.Name}}_tx_errs
`,
	"nic_drops": `CHART {{.ChartName}} '' 'NIC drop count' 'packets' {{.Net.Name}} 'packets' stacked
DIMENSION vm_{{.VmName}}_nic_{{.Net.Name}}_rx_drops
DIMENSION vm_{{.VmName}}_nic_{{.Net.Name}}_tx_drops
`,
	"block_traffic": `CHART {{.ChartName}} '' 'Block device traffic' 'bytes' {{.Block.Name}} 'traffic' stacked
DIMENSION vm_{{.VmName}}_drive_{{.Block.Name}}_rd_bytes
DIMENSION vm_{{.VmName}}_drive_{{.Block.Name}}_wr_bytes
`,
	"block_iops": `CHART {{.ChartName}} '' 'Block device operations' 'operations' {{.Block.Name}} 'iops' stacked
DIMENSION vm_{{.VmName}}_drv_{{.Block.Name}}_rd_ops
DIMENSION vm_{{.VmName}}_drv_{{.Block.Name}}_wr_ops
DIMENSION vm_{{.VmName}}_drv_{{.Block.Name}}_fl_ops
`,
	"block_times": `CHART {{.ChartName}} '' 'Block device time spent total' 'nanoseconds' {{.Block.Name}} 'iotime' stacked
DIMENSION vm_{{.VmName}}_drv_{{.Block.Name}}_rd_time
DIMENSION vm_{{.VmName}}_drv_{{.Block.Name}}_wr_time
DIMENSION vm_{{.VmName}}_drv_{{.Block.Name}}_fl_time
`,
}

var dataPointTemplates = map[string]string{
	"pcpu": `BEGIN {{.ChartName}} {{.Interval}}
SET vm_{{.VmName}}_pcpu_time = {{.Pcpu.Time}}
SET vm_{{.VmName}}_pcpu_user = {{.Pcpu.User}}
SET vm_{{.VmName}}_pcpu_sys = {{.Pcpu.System}}
END
`,
	"balloon": `BEGIN {{.ChartName}} {{.Interval}}
SET vm_{{.VmName}}_balloon_current = {{.Balloon.Current}}
SET vm_{{.VmName}}_balloon_maximum = {{.Balloon.Maximum}}
END
`,
	"nic_traffic": `BEGIN {{.ChartName}} {{.Interval}}
SET vm_{{.VmName}}_nic_{{.Net.Name}}_rx_bytes = {{.Net.RxBytes}}
SET vm_{{.VmName}}_nix_{{.Net.Name}}_tx_bytes = {{.Net.TxBytes}}
END
`,
	"nic_errors": `BEGIN {{.ChartName}} {{.Interval}}
SET vm_{{.VmName}}_nic_{{.Net.Name}}_rx_errs = {{.Net.RxErrs}})
SET vm_{{.VmName}}_nix_{{.Net.Name}}_tx_errs = {{.Net.TxErrs}}
END
`,
	"nic_drops": `BEGIN {{.ChartName}} {{.Interval}}
SET vm_{{.VmName}}_nic_{{.Net.Name}}_rx_drops = {{.Net.RxDrop}}
SET vm_{{.VmName}}_nix_{{.Net.Name}}_tx_drops = {{.Net.TxDrop}}
END
`,
	"block_traffic": `BEGIN {{.ChartName}} {{.Interval}}
SET vm_{{.VmName}}_drive_{{.Block.Name}}_rd_bytes = {{.Block.RdBytes}}
SET vm_{{.VmName}}_drive_{{.Block.Name}}_wr_bytes = {{.Block.WrBytes}}
END
`,
	"block_iops": `BEGIN {{.ChartName}} {{.Interval}}
SET vm_{{.VmName}}_drive_{{.Block.Name}}_rd_ops = {{.Block.RdReqs}}
SET vm_{{.VmName}}_drive_{{.Block.Name}}_wr_ops = {{.Block.WrReqs}}
SET vm_{{.VmName}}_drive_{{.Block.Name}}_fl_ops = {{.Block.FlReqs}}
END
`,
	"block_times": `BEGIN {{.ChartName}} {{.Interval}}
SET vm_{{.VmName}}_drive_{{.Block.Name}}_rd_time = {{.Block.RdTimes}}
SET vm_{{.VmName}}_drive_{{.Block.Name}}_wr_time = {{.Block.WrTimes}}
SET vm_{{.VmName}}_drive_{{.Block.Name}}_fl_time = {{.Block.FlTimes}}
END
`,
}

func (ch *Charts) updateFromTemplates(st *VMStats, key string) error {
	var err error
	templates, exists := ch.templates[st.ChartName]
	if !exists {
		templates.Graph = template.Must(template.New(fmt.Sprintf("%s graph", key)).Parse(graphTemplates[key]))
		err = templates.Graph.Execute(os.Stdout, st)
		if err != nil {
			// TODO: log
			return err
		}
		templates.DataPoint = template.Must(template.New(fmt.Sprintf("%s data point", key)).Parse(dataPointTemplates[key]))
		// TODO: what if fails?
		ch.templates[st.ChartName] = templates
	}
	return templates.DataPoint.Execute(os.Stdout, st)
}

func main() {
	if len(os.Args) != 2 {
		log.Fatalf("usage: %s interval", os.Args[0])
	}

	conf := Config{URI: "qemu:///system", IntervalSeconds: 1}
	confDir := os.Getenv("NETDATA_CONFIG_DIR")
	if confDir != "" {
		confPath := path.Join(confDir, confFile)
		err := conf.ReadFile(confPath)
		if err != nil {
			log.Fatalf("error reading the configuration file %s: %s", confPath, err)
		}
	}

	updateInterval := getInterval(conf)
	log.Printf("updating libvirt stats every %v", updateInterval)

	log.Printf("connecting to libvirt (%s)", conf.URI)
	conn, err := libvirt.NewConnectReadOnly(conf.URI)
	if err != nil {
		log.Fatalf("error connecing to libvirt (%s): %s", conf.URI, err)
		fmt.Printf("DISABLE\n")
		os.Exit(1)

	}
	defer conn.Close()

	log.Printf("connected to libvirt (%s)", conf.URI)

	c := time.Tick(updateInterval)
	charts := NewCharts()

	log.Printf("starting the collection loop")
	for now := range c {
		stats, err := conn.GetAllDomainStats(nil, 0, libvirt.CONNECT_GET_ALL_DOMAINS_STATS_ACTIVE)
		if err != nil {
			log.Printf("error collecting libvirt stats: %s", err)
			continue
		}
		// WARNING: we assume collection time is negligible

		charts.Update(now, stats)
	}
	log.Printf("collection stopped")
}
