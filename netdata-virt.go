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
		return errors.New(fmt.Sprint("invalid interval: %i", conf.IntervalSeconds))
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

func createCharts() {
	fmt.Printf("Here I should create the charts\n")
}

func collectValues() {
	fmt.Printf("Collecting!\n")
}

func printValues(dt time.Duration) {
	fmt.Printf("%v elapsed\n", dt)
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
	conn, err := libvirt.NewConnect(conf.URI)
	if err != nil {
		log.Fatalf("error connecing to libvirt (%s): %s", conf.URI, err)
		fmt.Printf("DISABLE\n")
		os.Exit(1)

	}
	defer conn.Close()

	log.Printf("connected to libvirt (%s)", conf.URI)
	createCharts()

	c := time.Tick(updateInterval)
	lastUpdate := time.Now()

	log.Printf("starting the collection loop")
	for now := range c {
		actualInterval := now.Sub(lastUpdate)
		lastUpdate = now

		collectValues()

		printValues(actualInterval)
	}
	log.Printf("collection stopped")
}
