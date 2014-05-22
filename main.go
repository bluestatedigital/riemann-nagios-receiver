package main

import (
    "log"
    "encoding/json"
    "fmt"
    "strconv"
    "strings"
    "flag"

    Redis "github.com/fzzy/radix/redis"
    "github.com/ActiveState/tail"
    StatsD "github.com/cactus/go-statsd-client/statsd"
)

var statsd StatsD.Statter

// flapjack event structure, a la v0.8.11.
type CheckResult struct {
    Entity  string `json:"entity"`
    Check   string `json:"check"`
    Type    string `json:"type"`
    State   string `json:"state"`
    Summary string `json:"summary"`
    Details string `json:"details"`
    Time    int    `json:"time"`
}

// explode when things go badly
func dieOnError(err error) {
    if err != nil {
        log.Fatal(err)
    }
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
    // checkPerfdata := lineBits[8]     // $SERVICEPERFDATA$, $HOSTPERFDATA$
    
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
        entity,        // entity
        check,         // check
        "service",     // type
        state,         // state
        checkOutput,   // summary
        details,       // details
        timestamp,     // time
    }, nil;
}

func followFile(file string, c chan *CheckResult) {
    tailer, err := tail.TailFile(file, tail.Config{
        ReOpen: true,
        Follow: true,
    })
    
    dieOnError(err)
    
    for line := range tailer.Lines {
        checkResult, err := parseNagLine(line.Text)
        
        if err != nil {
            log.Println(err)
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
    redisHost := flag.String("host", "", "redis hostname")
    redisPort := flag.Int("port", 6379, "redis port")
    redisDb   := flag.Int("db", 0, "redis database number")

    statsdHost := flag.String("statsd-host", "", "statsd hostname")
    statsdPort := flag.Int("statsd-port", 8125, "statsd port")
    
    verbose := flag.Bool("verbose", false, "be verbosey")
    
    flag.Parse()
    
    if len(*redisHost) == 0 {
        log.Fatal("must provide -host");
    }
    
    filenames := flag.Args()
    if len(filenames) == 0 {
        log.Fatal("must provide file(s) to follow")
    }
    
    if len(*statsdHost) > 0 {
        statsd, err = StatsD.New(fmt.Sprintf("%s:%d", *statsdHost, *statsdPort), "flapjack.nagios")
        dieOnError(err)
    } else {
        log.Println("using noop statsd client")
        statsd, err = StatsD.NewNoop()
    }

    redis, err := Redis.Dial("tcp", fmt.Sprintf("%s:%d", *redisHost, *redisPort))
    dieOnError(err)
    
    // close redis connection on exit
    defer redis.Close()
    
    dieOnError((*redis.Cmd("select", *redisDb)).Err)
    
    checkResultChan := make(chan *CheckResult, 10) // buffered
    
    for _, fn := range filenames {
        log.Printf("following %s", fn)
        
        go followFile(fn, checkResultChan)
    }
    
    // ensure tail cleans up on exit
    defer tail.Cleanup()
    
    for checkResult := range checkResultChan {
        checkResultJson, err := json.Marshal(checkResult)
        
        dieOnError(err)
        
        jsonStr := string(checkResultJson)
        
        if *verbose {
            log.Println(jsonStr)
        }
        
        // put new events on the *end* of the queue
        dieOnError((*redis.Cmd("rpush", "events", jsonStr)).Err)
        
        // keep in sync with flapjack-consul-receiver
        statsd.Inc("events", 1, 1.0)

        processedEvents += 1
    }
}
