// Jim Hughes
// Example code showing concurrency
// Twitter tweet API calls and parsing uses Arne Roomann-Kurrik's twittergo package
// 

package main

import (
    "fmt"
    "github.com/kurrik/oauth1a"
    "github.com/kurrik/twittergo"
    "io/ioutil"
    "net/http"
    "net/url"
    "os"
    "strings"
    "time"
    "flag"
    "regexp"
    "strconv"
    "sync"
)

var credsFile string
// Taken from the kurrik example code
func LoadCredentials() (client *twittergo.Client, err error) {
    credentials, err := ioutil.ReadFile(credsFile)
    if err != nil {
        log(INFO, fmt.Sprintf("Credentials read error: %v\n", err))
        return
    }
    lines := strings.Split(string(credentials), "\n")
    
    config := &oauth1a.ClientConfig{
        ConsumerKey:    lines[0],
        ConsumerSecret: lines[1],
    }
    client = twittergo.NewClient(config, nil)
    return
}

// define log level constants and a rudimentary logging function

const INFO  = 0
const DEBUG = 1
const TRACE = 2
var   runLogLevel int

func log (logLevel int, message string) {
    if (runLogLevel >= logLevel) {
    	logTime := time.Now().Format("15:04:05.99999999");
    	fmt.Printf("%v:%v\n", logTime, message)
    }
    return  
}

// add wrapper methods to the kurrik model
// to pull out the retweeted status information 
// this is to support better "uniqueness" of resulting tweet set

type RetweetedStatus map[string]interface{}

func (r RetweetedStatus) IdStr() string {
	return r["id_str"].(string)
}

type myTweet twittergo.Tweet
func (t myTweet) IsRetweet() bool {
	_, ok := t["retweeted_status"]
	return ok
}

func (t myTweet) RetweetedStatus() RetweetedStatus {
	return RetweetedStatus(t["retweeted_status"].(map[string]interface{}))
}

// our basic structure for what we want in terms of a tweet.
type TweetSummary struct {
    Id               string
    RetweetId        string
    Text             string
    UserName         string
    UserScreenName   string
    CreatedAt        string
    FinderName       string
}

// Support the fan-in pattern 
// Each finder will send tweetSummaries on a unique channel 
// This will also detect when all the finders are done
func mergeTweets(finderFeeds []<-chan TweetSummary) <-chan TweetSummary {
    var wg sync.WaitGroup
    toVetter := make(chan TweetSummary)
    
    output := func(feed <-chan TweetSummary) {
        defer wg.Done()
        for ts := range feed {
            toVetter <- ts 
        }
    }
    
    wg.Add(len(finderFeeds))
    for i, feed := range finderFeeds {
        log(DEBUG, fmt.Sprintf("MERGE: Firing up a twitter feed channel: %v", i))
        go output(feed)
    }

    // Start a goroutine to close out once all the output goroutines are done.
    go func() {
        log(DEBUG, fmt.Sprintf("MERGE: Starting to wait for everyone to be done"))
        wg.Wait()
        log(DEBUG, fmt.Sprintf("MERGE: Closing the toVetter channel"))
        close(toVetter)
    }()
    
    return toVetter
}

// This makes sure each tweet is unique.  If so, it passes the tweet on for writing
func VetTweets(incoming <-chan TweetSummary ) <-chan TweetSummary {

    writeChan := make(chan TweetSummary)
    
    uniqueTweetIds := make(map[string]string)
    
    go func() {
        log(DEBUG, fmt.Sprintf("VETTWEETS: starting to listen for tweets" ))
        for thisTweet := range incoming {
            tweetFinder, ok := uniqueTweetIds[thisTweet.Id]
            if ok == true {
               log(TRACE, fmt.Sprintf("VETTWEETS: received duplicate tweet id: %v Retweet Id: %v from %v initially reported by: %v:\n", thisTweet.Id, thisTweet.RetweetId, thisTweet.FinderName, tweetFinder ))
            } else {
               uniqueTweetIds[thisTweet.Id] = thisTweet.FinderName
               log(TRACE, fmt.Sprintf("VETTWEETS: received unique tweet id: %v Retweet Id: %v from %v initially reported by: %v:\n", thisTweet.Id, thisTweet.RetweetId, thisTweet.FinderName, tweetFinder ))
               writeChan <- thisTweet
           }
        }
        log(DEBUG, fmt.Sprintf("VETTWEETS: incoming channel has been closed" ))
        close(writeChan)
        return
    }()
    return writeChan
}

// write tweets to the specified file
// When we have enough tweets, tell the finders we are good
// When the input channel is closed, then tell the mainloop we are done
func WriteCSV(outputFile string, totalTweetCount int, writeChan <-chan TweetSummary, finderDone chan struct{}, mainDone chan bool) {  
    
    f, err := os.Create(outputFile)
    defer f.Close()
    if err != nil {
        fmt.Println(err)
        os.Exit(1)
    }
    
    headerLine := "ID, URL, RETWEET ID, TEXT, USER NAME, SCREEN NAME, CREATED, FINDER";
    _, err = fmt.Fprintln(f, headerLine)
    if err != nil {
        fmt.Println(err)
        os.Exit(1)
    }
    
    // Use these regular expressions to massage the text.  
    // Remove embedded linefeeds simply to make the .csv file readable in text editors
    // Change any embedded double-quotes (") to two double-quotes ("") per the RFC
    re := regexp.MustCompile("\n")
    re1 := regexp.MustCompile("\"")
    
    tweetCount    := 0
    log(DEBUG, fmt.Sprintf("WriteCSV starting to listen for %v tweets", totalTweetCount ))
    for thisTweet := range writeChan {
        
        if tweetCount >= totalTweetCount {
	    log(TRACE, fmt.Sprintf("WriteCSV flushing pipeline hit %v tweets id: %v from %v time: %v:\n", totalTweetCount, thisTweet.Id, thisTweet.FinderName, thisTweet.CreatedAt ))
	    continue
        }
        
        newText := re.ReplaceAllString(thisTweet.Text, " ")
        newText = re1.ReplaceAllString(newText, "\"\"")
        
        // Create a url to this tweet.  Makes looking the tweet up easier
        url := fmt.Sprintf("https://twitter.com/statuses/%s", thisTweet.Id);
        
        csvLine := fmt.Sprintf("%s,%s,%s,\"%s\",\"%s\",%s,\"%s\", %s", 
               thisTweet.Id, url, thisTweet.RetweetId, newText, thisTweet.UserName, thisTweet.UserScreenName, thisTweet.CreatedAt, thisTweet.FinderName)
        _, err = fmt.Fprintln(f, csvLine)
        if err != nil {
            fmt.Println(err)
            f.Close()
            os.Exit(1)
        }
        
        tweetCount++
        if tweetCount % 100 == 0 {
            log(INFO, fmt.Sprintf("WriteCSV has written %v tweets...", tweetCount))
        }
        if tweetCount == totalTweetCount {
            log(DEBUG, fmt.Sprintf("WriteCSV has hit %v tweets id: %v from %v time: %v:\n", totalTweetCount, thisTweet.Id, thisTweet.FinderName, thisTweet.CreatedAt ))
            close(finderDone)
        }
    }
    log(DEBUG, fmt.Sprintf("WriteCSV has completed"))
    
    // let the main loop know we have finished
    mainDone <- true
    return
}

// Each finder will start at midnight of a certain day and grab some number of tweets (batchSize)
// It continues to work backward until either:
// (1) it is told that we have the desired number of tweets - when the finderDone channel is closed, or
// (2) it doesn't get any results from the API call
// When it is done, it closes it's output channel, telling mergeTweets
// The API call and basic processing was taken from the kurrik enxample code

func FindTweets(myName, searchTerm, myDay, batchSize string, finderDone <-chan struct{}) <-chan TweetSummary{
    
    out := make(chan TweetSummary)
    
    go func() {
        defer close(out)
	    var (
	    err     error
	    client  *twittergo.Client
	    req     *http.Request
	    resp    *twittergo.APIResponse
	    results *twittergo.SearchResults
	    maxTwitterId    uint64
	    maxTwitterIdStr string
	    iterationTweetCount int
	    )
	    client, err = LoadCredentials()
	    if err != nil {
		log(INFO, fmt.Sprintf("Could not parse CREDENTIALS file: %v", err))
		os.Exit(1)
	    }
	    
	    // Start getting tweets for the assigned day
	    k := 0
	    for {
		k++
		log(DEBUG, fmt.Sprintf("Finder %v is starting iteration: %v", myName, k))
		
		// set up the url for the API call
		query := url.Values{}
		query.Set("q", searchTerm)
		query.Set("count", batchSize)
		query.Set("until", myDay)
		if maxTwitterId > 0 {
		    query.Set("max_id", maxTwitterIdStr)
		    log(DEBUG, fmt.Sprintf("Finder %v:  set max_id to: %v", myName, maxTwitterIdStr))
		}
		url := fmt.Sprintf("/1.1/search/tweets.json?%v", query.Encode())

		req, err = http.NewRequest("GET", url, nil)
		if err != nil {
		    log(INFO, fmt.Sprintf("ERROR: Finder %v: Could not parse request: %v", myName, err))
		    return
		}
		resp, err = client.SendRequest(req)
		if err != nil {
		    log(INFO, fmt.Sprintf("ERROR: Finder %v: Could not send request: %v", myName, err))
		    return
		}
		
		// We just got back the response and validated it
		// We need to see if we should keep going
		log(DEBUG, fmt.Sprintf("Finder %v: beginning to process iteration %v response", myName, k))
		select {
		    case <-finderDone:
                        log(DEBUG, fmt.Sprintf("Finder %v: iteration %v told things are done", myName, k))
                        return
                    default: 
                        results = &twittergo.SearchResults{}
                        err = resp.Parse(results)
			if err != nil {
			    log(INFO, fmt.Sprintf("Finder %v: Problem parsing response: %v", myName,err))
			    return
			}
        		
        		// OK, let's process these tweets
        		iterationTweetCount = 0
        		for _, tweet := range results.Statuses() {
            
				// Set up for next API call
            			if maxTwitterId == 0 || tweet.Id() < maxTwitterId {
                			maxTwitterId = tweet.Id()
                			maxTwitterIdStr = tweet.IdStr()
            			}
            
            			// Build the tweet summary 
            			ts := TweetSummary{}
            			ts.Id = tweet.IdStr()
            			ts.RetweetId = tweet.IdStr()
            			ts.Text = tweet.Text()
            			 
            			if myTweet(tweet).IsRetweet() {
                			ts.Id = myTweet(tweet).RetweetedStatus().IdStr()
            			}
            			ts.UserName = tweet.User().Name()
            			ts.UserScreenName = tweet.User().ScreenName()
            			ts.CreatedAt = tweet.CreatedAt().Format(time.RFC1123)
            			ts.FinderName = myName
            
            			out <- ts
            			iterationTweetCount++
            			
            			// Check if we are done again to minimize extra pipeline cruft
            			select {
				    case <-finderDone:
				        log(DEBUG, fmt.Sprintf("Finder %v: iteration %v told things are done after %v tweet send", myName, k, iterationTweetCount ))
				        return
                                    default: 
                                        continue
                                }
        		}
        		log(TRACE, fmt.Sprintf("Finder %v: iteration %v:  found %v tweets", myName, k, iterationTweetCount))
        
        		if iterationTweetCount == 0 {
            			log(INFO, fmt.Sprintf("Finder %v:  found no tweets in iteration %v\n", myName, k))
            			return
        		}
        	}
    }
    
    log(DEBUG, fmt.Sprintf("Finder %v:  is finished\n", myName))
    return
    }()
    
    return out
}

func main() {

    startTime := time.Now()
    log(INFO,fmt.Sprintf("Starting FindTweets"))
    
    hashPtr := flag.String("hash", "#IoT", "Search for this hashtag")
    outputFilePtr := flag.String("file", "tweets.csv", "Output file name")
    tweetCountPtr := flag.Int("count", 2000, "total number of tweets to collect")
    finderCountPtr := flag.Int("finder", 7, "Number of concurrent finder routines to start up")
    batchSizePtr := flag.Int("batch", 100, "Number of tweets to get per API call")
    runLogLevelPtr := flag.Int("log", 0, "Logging level 0 - 2")
    credsFileNamePtr := flag.String("creds", "CREDENTIALS.txt", "credentials file name")
    flag.Parse()
    log(INFO, fmt.Sprintf("Looking for hashtag: %v", *hashPtr))
    log(INFO, fmt.Sprintf("Looking for tweet count: %v", *tweetCountPtr))
    log(INFO, fmt.Sprintf("Writing tweets to: %v", *outputFilePtr))
    log(INFO, fmt.Sprintf("Number of finders to start: %v", *finderCountPtr))
    log(INFO, fmt.Sprintf("Batch size for API call: %v", *batchSizePtr))
    batchSize := strconv.Itoa(*batchSizePtr)
    runLogLevel = *runLogLevelPtr
    log(INFO, fmt.Sprintf("Logging Level: %v", runLogLevel))
    credsFile = *credsFileNamePtr
    log(INFO, fmt.Sprintf("Using credentials in: %v", credsFile ))
    
    doneChannel  := make(chan bool)
    finderDone   := make(chan struct{})
    
    var channels []<-chan TweetSummary
    todaysDate := startTime.AddDate(0,0,1)
    todayFormatted := fmt.Sprintf("%d-%02d-%02d", todaysDate.Year(), todaysDate.Month(), todaysDate.Day())
    log(DEBUG, fmt.Sprintf("Upper date range for retrieving Tweets from the API: %v", todayFormatted))
    var finderNames = []string{"Alpha", "Bravo", "Charlie", "Delta", "Echo", "Foxtrot", "Golf"}
    for i, finderName := range finderNames {
        myDay := todaysDate.AddDate(0, 0, -i)
        myDayFormatted := fmt.Sprintf("%d-%02d-%02d", myDay.Year(), myDay.Month(), myDay.Day())
        channels = append(channels, FindTweets(finderName, *hashPtr,  myDayFormatted, batchSize, finderDone))
        log(DEBUG, fmt.Sprintf("Started FindTweets %v (%v) looking at day %v:", finderName, i, myDayFormatted ))
        if i+1 == *finderCountPtr {
            break
        }
    }
    
    
    toVetter := mergeTweets(channels)
    writeChannel := VetTweets( toVetter )
    go WriteCSV(*outputFilePtr, *tweetCountPtr, writeChannel, finderDone, doneChannel)
    
    log(DEBUG, fmt.Sprintf("main: waiting for tweet completion"))
    <- doneChannel
    endTime := time.Now()
    elapsedTime := endTime.Sub(startTime)
    log(INFO, fmt.Sprintf("total time taken: %v seconds", elapsedTime.Seconds()))
    log(INFO, fmt.Sprintf("Completed FindTweets" ))
    os.Exit(0)

}