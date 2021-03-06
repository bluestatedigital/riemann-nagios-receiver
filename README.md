# riemann-nagios-receiver

Similar to the `flapjack-nagios-receiver` that is part of the
[Flapjack](http://flapjack.io) project, this utility reads from host and service
perfdata logs generated by Nagios, parses the records, and creates events in
Riemann for the check results.

Written in Go because deploying static binaries is easy.

## packaging

`build.sh` will create an RPM.  The `etc` directory contains a startup script.

## using

In your Nagios config:

    process_performance_data=1
    
    host_perfdata_file=/path/to/host-perfdata.log
    service_perfdata_file=/path/to/service-perfdata.log
    
    host_perfdata_file_template=[HOSTPERFDATA]\t$TIMET$\t$LASTHOSTCHECK$\t$HOSTNAME$\tHOST\t$HOSTSTATE$\t$HOSTOUTPUT$\t$HOSTPERFDATA$\t$LONGHOSTOUTPUT$
    service_perfdata_file_template=[SERVICEPERFDATA]\t$TIMET$\t$LASTSERVICECHECK$\t$HOSTNAME$\t$SERVICEDESC$\t$SERVICESTATE$\t$SERVICEOUTPUT$\t$SERVICEPERFDATA$\t$LONGSERVICEOUTPUT$
    
    host_perfdata_file_mode=a
    service_perfdata_file_mode=a

Run the receiver:

    riemann-nagios-receiver \
        -host <riemann_host> \
        -port <riemann_port> \
        <host_perfdata_file> <service_perfdata_file>

You may also specify `-statsd-host` and `-statsd-port` if you wish to record
metrics on the frequency of check outputs.

## colophon

My Second Go Project™
