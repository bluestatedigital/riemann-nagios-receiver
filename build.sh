#!/bin/bash

set -e -u -x

_basedir=$( cd -P "$( dirname "${BASH_SOURCE[0]}" )" && pwd )

# http://stackoverflow.com/a/17537385/53051
git update-index --refresh || echo "dirty working copy"

git_desc=$( git describe --dirty --always )

PKG_VER="${git_desc#v}" ## tag should be "va.b.c"
PKG_ITER="1"

tmpdir=$( mktemp -d -t gobuild.XXXXXX )
trap "echo removing ${tmpdir}; rm -rf ${tmpdir}" EXIT

export GOPATH=${tmpdir}/gopath
export GOBIN=${GOPATH}/bin

go get -d -v ./...
go build -v

pushd ${tmpdir}

mkdir -p opt/local/bin etc/rc.d/init.d etc/logrotate.d etc/sysconfig

cp ${_basedir}/etc/logrotate etc/logrotate.d/flapjack-nagios-receiver
cp ${_basedir}/etc/sysconfig etc/sysconfig/flapjack-nagios-receiver
cp ${_basedir}/etc/sysvinit.sh etc/rc.d/init.d/flapjack-nagios-receiver
cp ${_basedir}/flapjack-nagios-receiver opt/local/bin

popd
fpm \
    -s dir \
    -t rpm \
    -n flapjack-nagios-receiver \
    -v ${PKG_VER} \
    --iteration ${PKG_ITER} \
    --rpm-use-file-permissions \
    --config-files /etc/sysconfig/flapjack-nagios-receiver \
    -C ${tmpdir} \
    etc opt
