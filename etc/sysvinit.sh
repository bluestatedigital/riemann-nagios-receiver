#!/bin/bash
#
# riemann-nagios-receiver Manage riemann-nagios-receiver
#       
# chkconfig:   2345 95 95
# description: riemann-nagios-receiver pushes check status from Nagios to Riemann
# processname: riemann-nagios-receiver
# pidfile: /var/run/riemann-nagios-receiver.pid

### BEGIN INIT INFO
# Provides:       riemann-nagios-receiver
# Required-Start: $local_fs $network
# Required-Stop:
# Should-Start:
# Should-Stop:
# Default-Start: 2 3 4 5
# Default-Stop:  0 1 6
# Short-Description: Manage riemann-nagios-receiver
# Description: riemann-nagios-receiver pushes check status from Nagios to Riemann
### END INIT INFO

# source function library
. /etc/rc.d/init.d/functions

prog="riemann-nagios-receiver"
user="nag"
exec="/opt/local/bin/${prog}"
pidfile="/var/run/${prog}.pid"
lockfile="/var/lock/subsys/${prog}"
logfile="/var/log/${prog}.log"

# pull in sysconfig settings; REQUIRED
. /etc/sysconfig/${prog}

start() {
    [ -x $exec ] || exit 5
    
    ## check for required config
    if [ -z "${riemann_host}" ] || [ -z "${riemann_port}" ] || [ -z "${riemann_ttl_pad}" ] || [ -z "${nag_files}" ]; then
        exit 6
    fi
    
    opts="-host ${riemann_host}"
    opts="${opts} -port ${riemann_port}"
    opts="${opts} -ttl-pad ${riemann_ttl_pad}"

    if [ ! -z "${statsd_host}" ]; then
        opts="${opts} -statsd-host ${statsd_host}"
    fi

    if [ ! -z "${statsd_port}" ]; then
        opts="${opts} -statsd-port ${statsd_port}"
    fi

    ## must be last
    opts="${opts} ${nag_files}"

    umask 077

    touch $logfile $pidfile
    chown $user:$user $logfile $pidfile

    echo -n $"Starting ${prog}: "
    
    ## holy shell shenanigans, batman!
    ## daemon can't be backgrounded.  we need the pid of the spawned process,
    ## which is actually done via runuser thanks to --user.  you can't do "cmd
    ## &; action" but you can do "{cmd &}; action".
    daemon \
        --pidfile=${pidfile} \
        --user=${user} \
        " { ${exec} ${opts} &> ${logfile} 2>&1 & } ; echo \$! >| ${pidfile} "
    
    RETVAL=$?
    echo
    
    [ $RETVAL -eq 0 ] && touch $lockfile
    
    return $RETVAL
}

stop() {
    echo -n $"Shutting down ${prog}: "
    
    killproc -p ${pidfile} ${prog}
    RETVAL=$?
    
    [ $RETVAL -eq 0 ] && success || failure

    echo
    [ $RETVAL -eq 0 ] && rm -f ${lockfile} ${pidfile}
    return $RETVAL
}

restart() {
    stop
    start
}

rh_status() {
    status -p "${pidfile}" -l ${prog} ${exec}
    
    RETVAL=$?
    
    [ $RETVAL -eq 0 ] && ${exec} members
    
    return $RETVAL
}

rh_status_q() {
    rh_status >/dev/null 2>&1
}

case "$1" in
    start)
        rh_status_q && exit 0
        $1
        ;;
    stop)
        rh_status_q || exit 0
        $1
        ;;
    restart)
        $1
        ;;
    status)
        rh_status
        ;;
    condrestart|try-restart)
        rh_status_q || exit 0
        restart
        ;;
    *)
        echo $"Usage: $0 {start|stop|status|restart|condrestart|try-restart}"
        exit 2
esac

exit $?
