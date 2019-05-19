# findtweets

This program will search for a certain number of unique tweets using the Twitter Standard search API. It will write summary information
to a .csv file.  If it cannot find the requested number of tweets, it will write out those it did find, if any, and terminate gracefully.

The purpose of this exercise is to demonstrate concurrent programming.  The program architecture is described in later sections.

Building and Running
--------------------
I have used the twittergo and oauth1a packages from [Arne Roomann-Kurrik](https://github.com/kurrik?tab=repositories). 

You will need to install the following dependencies:

    go get -u github.com/kurrik/twittergo
    go get -u github.com/kurrik/oauth1a

You will also need Twitter Access Tokens for this program.  See [Twitter Access Tokens](https://developer.twitter.com/en/docs/basics/authentication/guides/access-tokens.html)
for more information.  Because this is a read-only application, only the consumer key and consumer secret are required.

Create a credentials file - the default is 
`CREDENTIALS.txt` in your project root, although this can be overriden.  The format of this file should be:

    <Twitter consumer key>
    <Twitter consumer secret>
    

Note: As the examples are written, the end of line char in the CREDENTIALS file
must be in UNIX format (LF and not CR+LF) otherwise the authentication fails.

Calling Flags
-------------

The program supports the following calling flags:

```
 -batch int Number of tweets to get per API call (default 100)
 
 -count int total number of tweets to collect (default 2000)
 
 -creds string credentials file name (default "CREDENTIALS.txt")
 
 -file string Output file name (default "tweets.csv")
 
 -finder int Number of concurrent finder routines to start up (default 7)
 
 -hash string Search for this hashtag (default "#IoT")
 
 -log int Logging level 0 - 2  (default 0)
    0 Informational - Basic information about the run, including the elapsed time
    1 Debug         - Information about the various go routines
    2 Trace         - Information regarding the tweets that are processed
 ```
  
About the Program
-------------------

The following sections describe the exercise requirements and the major program functionality.

Background
-------------

The exercise requirements are to use the Twitter API to find 2000 unique tweets with the hashtag "IoT" and write them to a .csv file.

The Twitter API will return a json formatted list of tweets meeting the search criteria. To determine uniqueness, I kept track of the 
Tweet Ids (more correctly the Tweet ID_String) in a map. In the case of a retweet, I used the original tweet ID.  This gave a _better_ set of unique tweets.

For the CSV file, I tried to follow [RFC4180](https://tools.ietf.org/html/rfc4180).  The RFC allows for newlines to be embedded in double-quoted fields,
this made looking at the .csv file in a text editor difficult, so I simply changed any newlines to spaces.

The program is designed to terminate:

1. When the requested number of unique tweets have been gathered and written, or
2. If all the Finders report they have found no more tweets for the given hash tag (or search term)

Finder - FindTweets
----------------------

The program will kick-off some number of "Finders".  Each Finder will be initialized with a date that serves as the latest time
for the search.  Because Twitter will support only 7 days worth of tweets, the default (and maximum) number of concurrent Finders is 7.

Each Finder will create a channel on which to send its Tweet Summaries to the MergeTweet routine.

The Finder will terminate when 
1. It is notified that we have found the required number of tweets.  This will be checked:
   A. After receiving a response from the API call, and
   B. After writing a Tweet Summary to its output channel.  The purpose is to terminate as cleanly as possible with few, if any, items in the pipeline.
2. The API returns no tweets.

When it terminates, it will close its output channel.

MergeTweets
--------------

The Merge Tweets routine implements the "fan-in" pattern.  

It will read the Tweet Summaries from the individual Finders and send them on to the VetTweets routine.

When all the Finder channels are closed, it will then close the VetTweets channel signalling Finder completion.

VetTweets
------------

The Vet Tweets routine receives Tweet Summaries from Merge Tweets.  It will check a list (map[string]string) containing the Tweet Ids.  If this is
a new tweet, Vet Tweets will add it to the list and forward it to WriteCSV.

When the incoming channel is closed, VetTweets will close the channel to WriteCSV.

WriteCSV
-----------

The Write CSV routine will write the tweets.csv file based on the incoming Tweet Summaries.  It will process the information:

1. Remove any newlines embedded in the Tweet text field
2. Change any double-quotes (") in the Tweet text field to double double-quotes("") per the RFC
3. COnstruct a Tweet URL - this was helpful in verifying the tweets have the search term and are unique.

WriteCSV is responsible for keeping track of the number of tweets that have been written.  When that number is reached, 
WriteCSV will close a channel thereby informing the Finders they are done.  When the incoming channel is closed, WriteCSV
will post true to the mainDone channel telling the main routine everything has been shut down gracefully.

