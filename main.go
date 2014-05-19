package main

import (
    "log"
    "encoding/json"
    "fmt"
    "strconv"
    "strings"

    // Redis "github.com/fzzy/radix/redis"
    "github.com/ActiveState/tail"
)

type CheckResult struct {
    Entity  string `json:"entity"`
    Check   string `json:"check"`
    Type    string `json:"type"`
    State   string `json:"state"`
    Summary string `json:"summary"`
    Details string `json:"details"`
    Time    int    `json:"time"`
}

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
    
    if err != nil {
        log.Fatal(err);
    }

    for line := range tailer.Lines {
        checkResult, err := parseNagLine(line.Text)
        
        if err != nil {
            log.Fatal(err);
        }
        
        c <- checkResult
    }
}

func main() {
    // redis, err = Redis.
    
    checkResultChan := make(chan *CheckResult, 10) // buffered
    go followFile("/Users/blalor/tmp/host-perfdata.log", checkResultChan)
    go followFile("/Users/blalor/tmp/service-perfdata.log", checkResultChan)
    
    for checkResult := range checkResultChan {
        _json, _ := json.Marshal(checkResult)
        fmt.Println(string(_json))
    }
}
