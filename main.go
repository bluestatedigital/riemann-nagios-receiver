package main

import (
    "fmt"
    "strconv"
    "strings"
    "flag"
    "os"

    "github.com/Sirupsen/logrus"
    
    Riemann "github.com/amir/raidman"
    "github.com/ActiveState/tail"
    StatsD "github.com/cactus/go-statsd-client/statsd"
)

var statsd StatsD.Statter
var logger = logrus.New()

// parses a Nagios perfdata line, per https://github.com/flapjack/flapjack/wiki/USING#configuring-nagios
func parseNagLine(line string) (*Riemann.Event, error) {
    /*
    host_perfdata_file_template=[HOSTPERFDATA]\t$TIMET$\t$HOSTNAME$\tHOST\t$HOSTSTATE$\t$HOSTEXECUTIONTIME$\t$HOSTLATENCY$\t$HOSTOUTPUT$\t$HOSTPERFDATA$
    service_perfdata_file_template=[SERVICEPERFDATA]\t$TIMET$\t$HOSTNAME$\t$SERVICEDESC$\t$SERVICESTATE$\t$SERVICEEXECUTIONTIME$\t$SERVICELATENCY$\t$SERVICEOUTPUT$\t$SERVICEPERFDATA$
    */
    
    lineBits := strings.Split(line, "\t")

    if len(lineBits) <= 8 {
        return nil, fmt.Errorf("Line doesn't split into at least 9 tab-separated strings: [%s]", line)
    }

    if lineBits[0] != "[HOSTPERFDATA]" && lineBits[0] != "[SERVICEPERFDATA]" {
        return nil, fmt.Errorf("rejecting this line as first string is neither '[HOSTPERFDATA]' nor '[SERVICEPERFDATA]': [%s]", line)
    }
    
    // $TIME$
    timestamp, err := strconv.ParseInt(lineBits[1], 10, 64)
    if err != nil {
        return nil, fmt.Errorf("rejecting this line as second string doesn't look like a timestamp: [%s]", line)
    }
    
    host    := lineBits[2] // $HOSTNAME$
    service := lineBits[3] // $SERVICEDESC$, "HOST"

    // $SERVICESTATE$, $HOSTSTATE$
    // A string indicating the current state of the service ("OK", "WARNING", "UNKNOWN", or "CRITICAL").
    // A string indicating the current state of the host ("UP", "DOWN", or "UNREACHABLE").
    state := strings.ToLower(lineBits[4])

    // $SERVICEEXECUTIONTIME$, $HOSTEXECUTIONTIME$
    // A (floating point) number indicating the number of seconds that the
    // (service|host) check took to execute (i.e. the amount of time the check
    // was executing).
    checkDuration := lineBits[5]
    
    // $SERVICELATENCY$, $HOSTLATENCY$
    // A (floating point) number indicating the number of seconds that a
    // scheduled (service|host) check lagged behind its scheduled check time.
    // For instance, if a check was scheduled for 03:14:15 and it didn't get
    // executed until 03:14:17, there would be a check latency of 2.0 seconds.
    // On-demand (service|host) checks have a latency of zero seconds.
    checkLatency := lineBits[6]
    
    // $SERVICEOUTPUT$, $HOSTOUTPUT$
    // The first line of text output from the last service check (i.e. "Ping
    // OK").
    details := lineBits[7]

    // $SERVICEPERFDATA$, $HOSTPERFDATA$
    // checkPerfdata := lineBits[8] 
    
    // $LONGSERVICEOUTPUT$
    // The full text output (aside from the first line) from the last service
    // check.
    // checkLongOutput := ""
    // if len(lineBits) >= 10 {
    //     details = details + "\n\n" + strings.Replace(lineBits[9], "\\n", "\n", -1)
    // }

    if state == "up" {
        state = "ok"
    }

    if state == "unreachable" {
        state = "unknown"
    }
    
    if state == "down" {
        state = "critical"
    }
    
    evt := Riemann.Event{
        Time:        timestamp,
        State:       state,
        Host:        host,
        Service:     service,
        Description: details,
        Tags:        []string{ "nagios" },
        Attributes:  make(map[string]string),
    }
    
    evt.Attributes["check_duration"] = checkDuration
    evt.Attributes["check_latency"]  = checkLatency
    
    return &evt, nil;
}

func followFile(file string, ttl int, c chan *Riemann.Event) {
    tailer, err := tail.TailFile(file, tail.Config{
        ReOpen: true,
        Follow: true,
        
        // start reading from the end of the file
        Location: &tail.SeekInfo{ 0, os.SEEK_END },
    })
    
    // die on error    
    if err != nil {
        logger.Fatalf("unable to open file: %s", err);
    }

    defer tailer.Stop()
    
    for line := range tailer.Lines {
        event, err := parseNagLine(line.Text)
        
        event.Ttl = float32(ttl)
        
        if err != nil {
            logger.Errorf("error tailing %s: %s", file, err)
            statsd.Inc("bad-lines", 1, 1.0)
        } else {
            c <- event
        }
    }
}

func main() {
    var err error
    
    // riemann host, port
    // files…
    riemannHost := flag.String("host", "",   "riemann hostname")
    riemannPort := flag.Int("port",    5555, "riemann port")
    ttl         := flag.Int("ttl",     300,  "riemann event ttl (seconds)")

    statsdHost := flag.String("statsd-host", "",   "statsd hostname")
    statsdPort := flag.Int("statsd-port",    8125, "statsd port")
    
    debug := flag.Bool("debug", false, "be verbosey")
    
    flag.Parse()
    
    if *debug == true {
        logger.Level = logrus.Debug
        logger.Info("debug enabled")
    }
    
    if len(*riemannHost) == 0 {
        logger.Fatal("must provide -host");
    }
    
    filenames := flag.Args()
    if len(filenames) == 0 {
        logger.Fatal("must provide file(s) to follow")
    }
    
    if len(*statsdHost) > 0 {
        statsd, err = StatsD.New(fmt.Sprintf("%s:%d", *statsdHost, *statsdPort), "riemann.nagios")
        
        if err != nil {
            logger.Fatalf("unable to create StatsD instance: %s", err)
        }
    } else {
        logger.Info("using noop statsd client")

        statsd, err = StatsD.NewNoop()
    }
    
    riemannConnStr := fmt.Sprintf("%s:%d", *riemannHost, *riemannPort)
    
    riemann, err := Riemann.Dial("tcp", riemannConnStr)
    if err != nil {
        logger.Fatalf("unable to connect to Riemann: %s", err)
    }
    
    eventChan := make(chan *Riemann.Event, 10) // buffered
    
    for _, fn := range filenames {
        logger.Infof("following %s", fn)
        
        go followFile(fn, *ttl, eventChan)
    }
    
    // ensure tail cleans up on exit
    defer tail.Cleanup()
    
    for event := range eventChan {
        logger.Debug(event)
        
        if riemann == nil {
            // connection was previously closed
            riemann, err = Riemann.Dial("tcp", riemannConnStr)
            
            if err != nil {
                logger.Errorf("unable to connect to Riemann: %s", err)
                continue
            }

            logger.Infof("reconnected to %s", riemannConnStr)
        }

        err := riemann.Send(event)

        if err == nil {
            // keep in sync with flapjack-nagios-receiver
            statsd.Inc("events", 1, 1.0)
        } else {
            logger.Errorf("error sending event to Riemann: %s; closing connection", err)
            
            riemann = nil
        }
    }
}
