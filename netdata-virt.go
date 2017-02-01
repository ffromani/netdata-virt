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
		return errors.New(fmt.Sprint("invalid interval: %d", conf.IntervalSeconds))
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

type Charts struct {
	created    map[string]bool
	lastUpdate time.Time
}

func NewCharts() Charts {
	return Charts{
		created:    make(map[string]bool),
		lastUpdate: time.Now(),
	}
}

func (ch *Charts) UpdateAll(now time.Time, domStats []libvirt.DomainStats) {
	actualInterval := now.Sub(ch.lastUpdate)
	ch.lastUpdate = now

	for _, domStat := range domStats {
		ch.Create(domStat)
		ch.Update(actualInterval, domStat)
	}
}

// fields documentation:
// https://github.com/firehol/netdata/wiki/External-Plugins
func (ch *Charts) Create(domStat libvirt.DomainStats) {
	var chartName string
	vmName, err := domStat.Domain.GetName()
	if err != nil {
		log.Printf("error collecting libvirt stats for Domain <>: %s", err)
		return
	}

	// TODO: how other plugins report cpu?
	chartName = fmt.Sprint("virt.vm_%s_pcpu_time", vmName)
	_, exists := ch.created[chartName]
	if !exists {
		fmt.Printf("CHART %s '' 'pcpu time spent' 'ns' 'pcpu' 'pcpu' stacked\n", chartName)
		fmt.Printf("DIMENSION vm_%s_pcpu_time total\n", vmName)
		fmt.Printf("DIMENSION vm_%s_pcpu_user user\n", vmName)
		fmt.Printf("DIMENSION vm_%s_pcpu_sys sys\n", vmName)
		ch.created[chartName] = true
	}
}

func (ch *Charts) Update(actualInterval time.Duration, domStat libvirt.DomainStats) {
	var chartName string
	vmName, err := domStat.Domain.GetName()
	if err != nil {
		log.Printf("error collecting libvirt stats for Domain <>: %s", err)
		return
	}

	netDataInterval := actualInterval.Nanoseconds() / 1000 // microseconds

	chartName = fmt.Sprint("virt.vm_%s_pcpu_time", vmName)
	fmt.Printf("BEGIN %s %d\n", chartName, netDataInterval)
	fmt.Printf("SET vm_%s_pcpu_time = %d\n", vmName, domStat.Cpu.Time)
	fmt.Printf("SET vm_%s_pcpu_user = %d\n", vmName, domStat.Cpu.User)
	fmt.Printf("SET vm_%s_pcpu_sys = %d\n", vmName, domStat.Cpu.System)
	fmt.Printf("END\n")
}

// TODO
// per-vm-balloon
// per-vm-per-nic stats
// per-vm-per-drive stats

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

		charts.UpdateAll(now, stats)
	}
	log.Printf("collection stopped")
}
