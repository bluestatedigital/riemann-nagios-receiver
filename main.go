package main

import (
    "encoding/json"
    "fmt"
    "strconv"
    "strings"
    "flag"
    "os"

    "github.com/Sirupsen/logrus"
    
    Redis "github.com/fzzy/radix/redis"
    "github.com/ActiveState/tail"
    StatsD "github.com/cactus/go-statsd-client/statsd"
)

var statsd StatsD.Statter
var logger = logrus.New()

// flapjack event structure, a la v0.8.11.
type CheckResult struct {
    Type     string `json:"type"`    // required
    State    string `json:"state"`   // required
    Entity   string `json:"entity"`  // required
    Check    string `json:"check"`   // required
    Summary  string `json:"summary"` // required
    
    Time     int    `json:"time"`
    Details  string `json:"details"`
    Perfdata string `json:"perfdata"`
}

// parses a Nagios perfdata line, per https://github.com/flapjack/flapjack/wiki/USING#configuring-nagios
func parseNagLine(line string) (*CheckResult, error) {
    lineBits := strings.Split(line, "\t")

    if len(lineBits) <= 8 {
        return nil, fmt.Errorf("Line doesn't split into at least 9 tab-separated strings: [%s]", line)
    }

    if lineBits[0] != "[HOSTPERFDATA]" && lineBits[0] != "[SERVICEPERFDATA]" {
        return nil, fmt.Errorf("rejecting this line as first string is neither '[HOSTPERFDATA]' nor '[SERVICEPERFDATA]': [%s]", line)
    }
    
    timestamp, err := strconv.Atoi(lineBits[1])
    if err != nil {
        return nil, fmt.Errorf("rejecting this line as second string doesn't look like a timestamp: [%s]", line)
    }
    
    entity        := lineBits[2]     // $HOSTNAME$
    check         := lineBits[3]     // $SERVICEDESC$, "HOST"
    state         := lineBits[4]     // $SERVICESTATE$, $HOSTSTATE$
    // checkTime     := lineBits[5]  // $SERVICEEXECUTIONTIME$, $HOSTEXECUTIONTIME$
    // checkLatency  := lineBits[6]  // $SERVICELATENCY$, $HOSTLATENCY$
    checkOutput   := lineBits[7]     // $SERVICEOUTPUT$, $HOSTOUTPUT$
    checkPerfdata := lineBits[8]     // $SERVICEPERFDATA$, $HOSTPERFDATA$
    
    checkLongOutput := ""
    if len(lineBits) >= 10 {
        checkLongOutput = strings.Replace(lineBits[9], "\\n", "\n", -1)
    }

    if strings.ToLower(state) == "up" {
        state = "ok"
    }

    if strings.ToLower(state) == "down" {
        state = "critical"
    }
    
    details := ""
    if len(checkLongOutput) > 0 {
        details = checkLongOutput
    }

    return &CheckResult{
        "service",     // Type
        state,         // State
        entity,        // Entity
        check,         // Check
        checkOutput,   // Summary
        timestamp,     // Time
        details,       // Details
        checkPerfdata, // Perfdata
    }, nil;
}

func followFile(file string, c chan *CheckResult) {
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
        checkResult, err := parseNagLine(line.Text)
        
        if err != nil {
            logger.Errorf("error tailing %s: %s", file, err)
            statsd.Inc("bad-lines", 1, 1.0)
        } else {
            c <- checkResult
        }
    }
}

func main() {
    var err error
    processedEvents := 0
    
    // redis host, port, db
    // files…
    redisHost := flag.String("host", "",   "redis hostname")
    redisPort := flag.Int("port",    6379, "redis port")
    redisDb   := flag.Int("db",      0,    "redis database number")

    statsdHost := flag.String("statsd-host", "",   "statsd hostname")
    statsdPort := flag.Int("statsd-port",    8125, "statsd port")
    
    debug := flag.Bool("debug", false, "be verbosey")
    
    flag.Parse()
    
    if *debug == true {
        logger.Level = logrus.Debug
        logger.Info("debug enabled")
    }
    
    if len(*redisHost) == 0 {
        logger.Fatal("must provide -host");
    }
    
    filenames := flag.Args()
    if len(filenames) == 0 {
        logger.Fatal("must provide file(s) to follow")
    }
    
    if len(*statsdHost) > 0 {
        statsd, err = StatsD.New(fmt.Sprintf("%s:%d", *statsdHost, *statsdPort), "flapjack.nagios")
        
        if err != nil {
            logger.Fatalf("unable to create StatsD instance: %s", err)
        }
    } else {
        logger.Info("using noop statsd client")

        statsd, err = StatsD.NewNoop()
    }
    
    redisConnStr := fmt.Sprintf("%s:%d", *redisHost, *redisPort)
    
    redis, err := Redis.Dial("tcp", redisConnStr)
    if err != nil {
        logger.Fatalf("unable to connect to Redis: %s", err)
    }
    
    err = (*redis.Cmd("select", *redisDb)).Err
    if err != nil {
        logger.Fatalf("unable to select db: %s", err)
    }
    
    checkResultChan := make(chan *CheckResult, 10) // buffered
    
    for _, fn := range filenames {
        logger.Infof("following %s", fn)
        
        go followFile(fn, checkResultChan)
    }
    
    // ensure tail cleans up on exit
    defer tail.Cleanup()
    
    for checkResult := range checkResultChan {
        checkResultJson, err := json.Marshal(checkResult)
        
        if err != nil {
            logger.Fatalf("unable to serialize check result %s: %s", checkResult, err)
        }
        
        jsonStr := string(checkResultJson)
        
        logger.Debug(jsonStr)
        
        if redis == nil {
            // connection was previously closed
            redis, err = Redis.Dial("tcp", redisConnStr)
            
            if err != nil {
                logger.Errorf("unable to connect to Redis: %s", err)
                continue
            }

            logger.Infof("reconnected to %s", redisConnStr)
        }

        reply := redis.Cmd("lpush", "events", jsonStr)
        if reply.Err == nil {
            // keep in sync with flapjack-consul-receiver
            statsd.Inc("events", 1, 1.0)
        } else {
            logger.Errorf("error pushing to 'events' queue: %s; closing connection", reply.Err)
            
            redis.Close() // this returns an error, but seriously?
            redis = nil
        }
        
        processedEvents += 1
    }
}
