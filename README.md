netdata-virt2
============
virt plugin for [netdata](https://github.com/netdata/netdata).

The original netdata-virt can be found [here](https://github.com/fromanirh/netdata-virt).

Used the formula by virt-manager.

So you can check resource usage similar to virt-manager.

GPLv2 licensed.

More to come.

Installation
------------

Depends on libvirt-devel package.

    Q. How to build on CentOS 7.x.
    # yum install -y go libvirt-devel
    # cd netdata-virt
    # go mod init netdata-virt
    # go mod tidy
    # go build -v

    Q. How to build on CentOS 8.x.
    # dnf install -y go libvirt-devel
    # cd netdata-virt
    # go get github.com/libvirt/libvirt-go
    # go build -v

    Q. How to run test mode.
    # ./netdata-virt <INTERVAL_SECONDS>

    Q. netdata plugin settings
    # cp netdata-virt /usr/libexec/netdata/plugins.d/virt.plugin
    # vi /etc/netdata/netdata.conf
    ...
    [plugins]
        virt = yes   <-- add this line & :wq!
        tc = no
        idlejitter = no
        ...
    # systemctl restart netdata

Check to netdata web page

![screenshot1](/doc/virt1.png)

![screenshot2](/doc/virt2.png)
