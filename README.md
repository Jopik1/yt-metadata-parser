Youtube metadata parser in Golang, based on YouTube-MA https://github.com/CorentinB/YouTube-MA

Works with multiple threads, parses the watch page and produces files in json format

Dependencies

```
$ go get github.com/PuerkitoBio/goquery
$ go get github.com/labstack/gommon/color
$ go get github.com/spf13/cast
$ go get golang.org/x/net/html
$ go get golang.org/x/net/proxy
```

run with:

```
$ go build
$ ./yt-metadata-parser -Concurrency=16 -MasterServer=http://ytc.bot.nu
```


Concurrency specifies how many concurent connections will be made to youtube.
