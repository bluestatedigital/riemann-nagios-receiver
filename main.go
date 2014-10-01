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

// parses a Nagios perfdata line, inspired by
// https://github.com/flapjack/flapjack/wiki/USING#configuring-nagios
func parseNagLine(line string, ttlPad int, minTtl int) (*Riemann.Event, error) {
    /* tab-delimited
    host_perfdata_file_template=   [HOSTPERFDATA]     $TIMET$ $LASTHOSTCHECK$    $HOSTNAME$ HOST          $HOSTSTATE$    $HOSTOUTPUT$    $HOSTPERFDATA$    $LONGHOSTOUTPUT$
    service_perfdata_file_template=[SERVICEPERFDATA]  $TIMET$ $LASTSERVICECHECK$ $HOSTNAME$ $SERVICEDESC$ $SERVICESTATE$ $SERVICEOUTPUT$ $SERVICEPERFDATA$ $LONGSERVICEOUTPUT$
                                   0                  1       2                  3          4             5              6               7                 8
    */
    
    // split into at most 9 tokens
    tokens := strings.SplitN(line, "\t", 9)

    if len(tokens) <= 8 {
        return nil, fmt.Errorf("Line doesn't split into at least 9 tab-separated strings: [%s]", line)
    }

    if tokens[0] != "[HOSTPERFDATA]" && tokens[0] != "[SERVICEPERFDATA]" {
        return nil, fmt.Errorf("rejecting this line as first string is neither '[HOSTPERFDATA]' nor '[SERVICEPERFDATA]': [%s]", line)
    }
    
    // $TIME$
    timestamp, err := strconv.ParseInt(tokens[1], 10, 0)
    if err != nil {
        return nil, fmt.Errorf("rejecting this line as second string doesn't look like a timestamp: [%s]", line)
    }
    
    // $LASTHOSTCHECK$, $LASTSERVICECHECK$
    lastCheckTime, err := strconv.ParseInt(tokens[2], 10, 0)
    if err != nil {
        return nil, fmt.Errorf("rejecting this line as third string doesn't look like a timestamp: [%s]", line)
    } 
    
    // TTL based on *last* check; whatev.  there's no way in nag to provide the
    // check interval as a macro, and our config has intervals betwen 1 minute
    // and 1 hour.
    ttl := int(timestamp) - int(lastCheckTime)
    
    if (ttl < minTtl) {
        ttl = minTtl
    }
    
    host    := tokens[3] // $HOSTNAME$
    service := tokens[4] // $SERVICEDESC$, "HOST"

    // $SERVICESTATE$, $HOSTSTATE$
    // A string indicating the current state of the service ("OK", "WARNING", "UNKNOWN", or "CRITICAL").
    // A string indicating the current state of the host ("UP", "DOWN", or "UNREACHABLE").
    state := strings.ToLower(tokens[5])

    // $SERVICEOUTPUT$, $HOSTOUTPUT$
    // The first line of text output from the last service check (i.e. "Ping
    // OK").
    details := tokens[6]

    // $SERVICEPERFDATA$, $HOSTPERFDATA$
    checkPerfdata := strings.TrimSpace(tokens[7])
    
    // $LONGSERVICEOUTPUT$, $LONGHOSTOUTPUT$
    // The full text output (aside from the first line) from the last service
    // check.
    if len(tokens) == 9 {
        longOutput := strings.TrimSpace(strings.Replace(tokens[8], "\\n", "\n", -1))
        
        if len(longOutput) > 0 {
            details = details + "\n\n" + longOutput
        }
    }

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
        Ttl:         float32(ttl * ttlPad),
        State:       state,
        Host:        host,
        Service:     service,
        Description: details,
        Tags:        []string{ "nagios" },
        Attributes:  map[string]string{
            "perfdata": checkPerfdata,
        },
    }
    
    return &evt, nil;
}

func followFile(file string, ttlPad int, minTtl int, c chan *Riemann.Event) {
    tailer, err := tail.TailFile(file, tail.Config{
        ReOpen: true,
        Follow: true,

        // I think I'm running into https://github.com/ActiveState/tail/issues/21
        Poll: true,
        
        // start reading from the end of the file
        Location: &tail.SeekInfo{ 0, os.SEEK_END },
    })
    
    // die on error    
    if err != nil {
        logger.Fatalf("unable to open file: %s", err);
    }

    defer tailer.Stop()
    
    for line := range tailer.Lines {
        event, err := parseNagLine(line.Text, ttlPad, minTtl)
        
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
    minTtl      := flag.Int("min-ttl", 60,   "minimum ttl")
    ttlPad      := flag.Int("ttl-pad", 3,    "ttl multiplier")

    statsdHost := flag.String("statsd-host", "",   "statsd hostname")
    statsdPort := flag.Int("statsd-port",    8125, "statsd port")
    
    debug := flag.Bool("debug", false, "be verbosey")
    
    flag.Parse()
    
    if *debug == true {
        logger.Level = logrus.DebugLevel
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
        
        go followFile(fn, *ttlPad, *minTtl, eventChan)
    }
    
    // ensure tail cleans up on exit
    defer tail.Cleanup()
    
    for event := range eventChan {
        logger.Debugf("%+v", event)
        
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
