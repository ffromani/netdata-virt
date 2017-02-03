netdata-virt
============
(c) 2017 Red Hat inc.

virt plugin for [netdata](https://github.com/firehol/netdata).

Let you monitor your libvirt-managed VMs.

GPLv2 licensed.

More to come.

Screenshots
-----------

![screenshot1](/doc/virt1.png)

![screenshot2](/doc/virt2.png)


Installation
------------

    
    $ cd netdata-virt
    $ go build -v .
    # cp netdata-virt /usr/libexec/netdata/plugins.d/virt.plugin
    # systemctl restart netdata
    

(yes, that's it)
