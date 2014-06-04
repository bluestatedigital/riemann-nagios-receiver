#!/bin/bash

exec docker run \
    -i \
    -t \
    -v $PWD:/srv/flapjack-nagios-receiver \
    -w /srv/flapjack-nagios-receiver \
    blalor/centos-buildtools \
    sh -c 'yum install -y golang bzr && ./build.sh'
