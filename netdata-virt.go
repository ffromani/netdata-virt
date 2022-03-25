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
    "strconv"
    "text/template"
    "time"
    "math"
)

// specifications and skeleton plugin:
// https://github.com/firehol/netdata/wiki/External-Plugins

const confFile string = "netdata-virt.json"

type ChartConfig struct {
    Graph     string
    DataPoint string
}

type ChartTemplates struct {
    Graph     *template.Template
    DataPoint *template.Template
}

// fields documentation:
// https://github.com/firehol/netdata/wiki/External-Plugins
// CHART, DIMENSION, SET format document : https://learn.netdata.cloud/docs/agent/collectors/plugins.d
var templateConfig = map[string]ChartConfig {
    "vcpu": ChartConfig{
        Graph: `CHART {{.ChartName}} '' 'vcpu usage' 'percentage' 'vcpu' 'vcpu' area
        DIMENSION vm_{{.VmName}}_vcpu_usage usage absolute '' 100
        `,
        DataPoint: `BEGIN {{.ChartName}} {{.Interval}}
        SET vm_{{.VmName}}_vcpu_usage = {{.VcpuUsage}}
        END
        `,
    },
    "memory": ChartConfig{
        Graph: `CHART {{.ChartName}} '' 'memory usage' 'percentage' 'memory' 'memory' area
        DIMENSION vm_{{.VmName}}_memory_usage usage absolute '' 100
        `,
        DataPoint: `BEGIN {{.ChartName}} {{.Interval}}
        SET vm_{{.VmName}}_memory_usage = {{.MemUsage}}
        END
        `,
    },
    "network": ChartConfig{
        Graph: `CHART {{.ChartName}} '' 'Network traffic' 'bytes' 'network' 'network' area
        DIMENSION vm_{{.VmName}}_total_in_bytes
        DIMENSION vm_{{.VmName}}_total_out_bytes
        `,
        DataPoint: `BEGIN {{.ChartName}} {{.Interval}}
        SET vm_{{.VmName}}_total_in_bytes = {{.TotalRxBytes}}
        SET vm_{{.VmName}}_total_out_bytes = {{.TotalTxBytes}}
        END
        `,
    },
    "storage": ChartConfig{
        Graph: `CHART {{.ChartName}} '' 'Block device traffic' 'bytes' 'storage' 'storage' area
        DIMENSION vm_{{.VmName}}_total_read_bytes
        DIMENSION vm_{{.VmName}}_total_write_bytes
        `,
        DataPoint: `BEGIN {{.ChartName}} {{.Interval}}
        SET vm_{{.VmName}}_total_read_bytes = {{.TotalRdBytes}}
        SET vm_{{.VmName}}_total_write_bytes = {{.TotalWrBytes}}
        END
        `,
    },
}

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

    val, err := strconv.Atoi(os.Args[1])
    intv.Cmdline = time.Duration(val) * time.Second
    if err != nil {
        return err
    }
    if envVar != "" {
        val, err = strconv.Atoi(envVar)
        if err != nil {
            return err
        }
        intv.Environ = time.Duration(val) * time.Second
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
    ChartName       string
    VmName          string
    Interval        int64
    VcpuUsage       float64
    MemUsage        float64
    TotalRxBytes    uint64
    TotalTxBytes    uint64
    TotalRdBytes  uint64
    TotalWrBytes uint64
    Pcpu            *libvirt.DomainStatsCPU
    Balloon         *libvirt.DomainStatsBalloon
}

func (ch *Charts) Update(now time.Time, domStats []libvirt.DomainStats, prevDomStats []libvirt.DomainStats, node *libvirt.NodeInfo) {
    var err error
    actualInterval := now.Sub(ch.lastUpdate)
    ch.lastUpdate = now

    for _, domStat := range domStats {
        vmStats := VMStats{
            ChartName:    "",
            VmName:       "",
            Interval:     actualInterval.Nanoseconds() / 1000, // microseconds
            VcpuUsage:    0.00,
            MemUsage:     0.00,
            TotalRxBytes: 0,
            TotalTxBytes: 0,
            TotalRdBytes: 0,
            TotalWrBytes: 0,
            Pcpu:         domStat.Cpu,
            Balloon:      domStat.Balloon,
        }
        vmStats.VmName, err = domStat.Domain.GetName()
        if err != nil {
            log.Printf("error collecting libvirt stats for Domain <>: %s", err)
            continue
        }

        if prevDomStats != nil {
            for _, prevDomStat := range prevDomStats {
                var prevVmName, err = prevDomStat.Domain.GetName()
                if err != nil {
                    log.Printf("error collecting libvirt stats for Domain <>: %s", err)
                    continue
                }

                if vmStats.VmName == prevVmName {
                    //Calculate vCPU
                    //Ref. : https://github.com/virt-manager/virt-manager/blob/main/virtManager/lib/statsmanager.py#L178
                    var pcentbase = (float64(vmStats.Pcpu.Time - prevDomStat.Cpu.Time) * 100) / (math.Round(float64(vmStats.Interval) * 1000))
                    //var cpuHostPercent = pcentbase / float64(node.Cpus)
                    var cpuGuestPercent = pcentbase / float64(len(domStat.Vcpu))
                    if cpuGuestPercent >= 100 {
                        cpuGuestPercent = 100.00
                    }
                    vmStats.VcpuUsage = float64(cpuGuestPercent * 100)

                    //Calculate Network Traffic
                    var currTotalRxBytes uint64
                    var currTotalTxBytes uint64

                    for _, currDomNetStat := range domStat.Net {
                        currTotalRxBytes += currDomNetStat.RxBytes
                        currTotalTxBytes += currDomNetStat.TxBytes
                    }

                    var prevTotalRxBytes uint64
                    var prevTotalTxBytes uint64

                    for _, prevDomNetStat := range prevDomStat.Net {
                        prevTotalRxBytes += prevDomNetStat.RxBytes
                        prevTotalTxBytes += prevDomNetStat.TxBytes
                    }

                    vmStats.TotalRxBytes = currTotalRxBytes - prevTotalRxBytes
                    vmStats.TotalTxBytes = currTotalTxBytes - prevTotalTxBytes

                    //Calculate Block Traffic
                    var currTotalRdBytes uint64
                    var currTotalWrBytes uint64

                    for _, currDomBlockStat := range domStat.Block {
                        currTotalRdBytes += currDomBlockStat.RdBytes
                        currTotalWrBytes += currDomBlockStat.WrBytes
                    }

                    var prevTotalRdBytes uint64
                    var prevTotalWrBytes uint64

                    for _, prevDomBlockStat := range prevDomStat.Block {
                        prevTotalRdBytes += prevDomBlockStat.RdBytes
                        prevTotalWrBytes += prevDomBlockStat.WrBytes
                    }

                    vmStats.TotalRdBytes = currTotalRdBytes - prevTotalRdBytes
                    vmStats.TotalWrBytes = currTotalWrBytes - prevTotalWrBytes
                    break
                }
            }
        }

        //Calculate Memory
        var curMem = float64(vmStats.Balloon.Current - vmStats.Balloon.Unused)
        var currMemPercent = (curMem / float64(vmStats.Balloon.Current) * 100)

        if currMemPercent >= 100 {
            currMemPercent = 100.00
        }
        vmStats.MemUsage = float64(currMemPercent * 100)

        vmStats.ChartName = fmt.Sprintf("virt.vm_%s_vcpu_usage", vmStats.VmName)
        ch.updateFromTemplates(&vmStats, "vcpu")

        vmStats.ChartName = fmt.Sprintf("virt.vm_%s_memory_usage", vmStats.VmName)
        ch.updateFromTemplates(&vmStats, "memory")

        vmStats.ChartName = fmt.Sprintf("virt.vm_%s_total_network_traffic", vmStats.VmName)
        ch.updateFromTemplates(&vmStats, "network")

        vmStats.ChartName = fmt.Sprintf("virt.vm_%s_total_storage_traffic", vmStats.VmName)
        ch.updateFromTemplates(&vmStats, "storage")
    }
}

func (ch *Charts) updateFromTemplates(st *VMStats, key string) error {
    var err error
    templates, exists := ch.templates[st.ChartName]
    if !exists {
        tmplConf, ok := templateConfig[key]
        if !ok {
            err = errors.New(fmt.Sprintf("no template for '%s'", key))
            log.Printf("error getting the template config for %s: %s", key, err)
            return err
        }
        templates.Graph, err = template.New(fmt.Sprintf("%s graph", key)).Parse(tmplConf.Graph)
        if err != nil {
            log.Printf("error setting up the graph template for %s: %s", key, err)
            return err
        }
        err = templates.Graph.Execute(os.Stdout, st)
        if err != nil {
            log.Printf("error executing the graph template for %s: %s", key, err)
            return err
        }
        templates.DataPoint, err = template.New(fmt.Sprintf("%s data point", key)).Parse(tmplConf.DataPoint)
        if err != nil {
            log.Printf("error setting up the sampling template for %s: %s", key, err)
            return err
        }
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
            log.Printf("warning... error reading the configuration file %s: %s", confPath, err)
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

    var prev []libvirt.DomainStats
    node, err := conn.GetNodeInfo()
    if err != nil {
        log.Fatalf("error collecting host stats: %s", err)
        fmt.Printf("DISABLE\n")
        os.Exit(1)
    }

    log.Printf("starting the collection loop")
    for now := range c {
        stats, err := conn.GetAllDomainStats(nil, 0, libvirt.CONNECT_GET_ALL_DOMAINS_STATS_ACTIVE)
        if err != nil {
            log.Printf("error collecting libvirt stats: %s", err)
            continue
        }
        // WARNING: we assume collection time is negligible

        charts.Update(now, stats, prev, node)
        prev = stats
    }
    log.Printf("collection stopped")
}
