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
		vmName, err := domStat.Domain.GetName()
		if err != nil {
			log.Printf("error collecting libvirt stats for Domain <>: %s", err)
			continue
		}
		interval := actualInterval.Nanoseconds() / 1000 // microseconds

		ch.UpdatePCPU(vmName, interval, domStat.Cpu)
	}
}

// fields documentation:
// https://github.com/firehol/netdata/wiki/External-Plugins

func (ch *Charts) UpdatePCPU(vmName string, interval int64, pcpu *libvirt.DomainStatsCPU) {
	chartName := fmt.Sprintf("virt.vm_%s_pcpu_time", vmName)
	_, exists := ch.created[chartName]
	if !exists {
		fmt.Printf("CHART %s '' 'pcpu time spent' 'ns' 'pcpu' 'cputime' stacked\n", chartName)
		fmt.Printf("DIMENSION vm_%s_pcpu_time total\n", vmName)
		fmt.Printf("DIMENSION vm_%s_pcpu_user user\n", vmName)
		fmt.Printf("DIMENSION vm_%s_pcpu_sys sys\n", vmName)
		ch.created[chartName] = true
	}

	fmt.Printf("BEGIN %s %d\n", chartName, interval)
	fmt.Printf("SET vm_%s_pcpu_time = %d\n", vmName, pcpu.Time)
	fmt.Printf("SET vm_%s_pcpu_user = %d\n", vmName, pcpu.User)
	fmt.Printf("SET vm_%s_pcpu_sys = %d\n", vmName, pcpu.System)
	fmt.Printf("END\n")
}

func (ch *Charts) UpdateMemBalloon(vmName string, interval int64, balloon *libvirt.DomainStatsBalloon) {
	chartName := fmt.Sprintf("virt.vm_%s_balloon", vmName)
	_, exists := ch.created[chartName]
	if !exists {
		fmt.Printf("CHART %s '' 'balloon size' 'kiB' 'balloon' 'ram' stacked\n", chartName)
		fmt.Printf("DIMENSION vm_%s_balloon_current current\n", vmName)
		fmt.Printf("DIMENSION vm_%s_balloon_maximum maximum\n", vmName)
		ch.created[chartName] = true
	}

	fmt.Printf("BEGIN %s %d\n", chartName, interval)
	fmt.Printf("SET vm_%s_balloon_current = %d\n", vmName, balloon.Current)
	fmt.Printf("SET vm_%s_balloon_maximum = %d\n", vmName, balloon.Maximum)
	fmt.Printf("END\n")
}

// TODO
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
