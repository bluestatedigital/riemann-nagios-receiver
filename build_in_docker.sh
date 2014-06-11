#!/bin/bash

exec docker run \
    -i \
    -t \
    -v $PWD:/srv/riemann-nagios-receiver \
    -w /srv/riemann-nagios-receiver \
    blalor/centos-buildtools \
    sh -c 'yum install -y golang bzr && ./build.sh'
