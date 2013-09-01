package gosync

import (
    "crypto/md5"
    "fmt"
    "io/ioutil"
    "os"
    "path/filepath"
    "strings"
    "launchpad.net/goamz/aws"
    "launchpad.net/goamz/s3"
)

type SyncPair struct {
    Source string
    Target string
    Auth aws.Auth
    Concurrent int
}

func (s *SyncPair) Sync() bool {
    if s.validPair() != true {
        fmt.Printf("Target or source not valid.\n")
        return false
    }

    if validS3Url(s.Source) {
       return s.syncS3ToDir()
    } else {
       return s.syncDirToS3()
    }
}

func lookupBucket(bucketName string, auth aws.Auth) *s3.Bucket {
    var bucket s3.Bucket
    for r, _ := range aws.Regions {
        fmt.Printf("Looking for bucket in %s.\n", r)
        s3 := s3.New(auth, aws.Regions[r])
        b := s3.Bucket(bucketName)
        _, err := b.List("","","",0)
        if err == nil {
            bucket = *b
            fmt.Printf("Found bucket in %s.\n", r)
        } else if err.Error() == "Get : 301 response missing Location header" {
            continue
        }
    }
    return &bucket
}

func (s *SyncPair) syncDirToS3() bool {
    sourceFiles := loadLocalFiles(s.Source)
    targetFiles := loadS3Files(s.Target, s.Auth)

    var routines []chan string

    s3url := S3Url{Url: s.Target}
    bucket := lookupBucket(s3url.Bucket(), s.Auth)

    count := 0
    for file, _ := range sourceFiles {
        if targetFiles[file] != sourceFiles[file] {
            count++
            filePath := strings.Join([]string{s.Source, file}, "/")
            fmt.Printf("Starting sync: %s -> s3://%s/%s.\n", filePath, bucket.Name, file)
            wait := make(chan string)
            keyPath := strings.Join([]string{s3url.Key(), file}, "/")
            go putRoutine(wait, filePath, bucket, keyPath)
            routines = append(routines, wait)
        }
        if count > s.Concurrent {
            fmt.Printf("Maxiumum concurrent threads running. Waiting.\n")
            waitForRoutines(routines)
            count = 0
            routines = routines[0:0]
        }
    }
    waitForRoutines(routines)
    return true
}

func (s *SyncPair) syncS3ToDir() bool {
    sourceFiles := loadS3Files(s.Source, s.Auth)
    targetFiles := loadLocalFiles(s.Target)

    var routines []chan string

    s3url := S3Url{Url: s.Source}
    bucket := lookupBucket(s3url.Bucket(), s.Auth)

    count := 0

    for file, _ := range sourceFiles {
        if targetFiles[file] != sourceFiles[file] {
            count++
            filePath := strings.Join([]string{s.Target, file}, "/")
            fmt.Printf("Starting sync: s3://%s/%s -> %s.\n", bucket.Name, file, filePath)
            if filepath.Dir(filePath) != "." {
               err := os.MkdirAll(filepath.Dir(filePath), 0755)
               if err != nil {
                  panic(err.Error())
               }
            }

            wait := make(chan string)
            go getRoutine(wait, filePath, bucket, file)
            routines = append(routines, wait)
        }
        if count > s.Concurrent {
            fmt.Printf("Maxiumum concurrent threads running. Waiting.\n")
            waitForRoutines(routines)
            count = 0
            routines = routines[0:0]
        }
    }
    waitForRoutines(routines)
    return true
}

func loadS3Files(url string, auth aws.Auth) map[string]string {
          files := map[string]string{}
          s3url := S3Url{Url: url}
          path  := s3url.Path()

          bucket := lookupBucket(s3url.Bucket(), auth)

          data, err := bucket.List(path, "", "", 0)
          if err != nil {
             panic(err.Error())
          }
          for i := range data.Contents {
            md5sum := strings.Trim(data.Contents[i].ETag, "\"")
            k := strings.TrimPrefix(data.Contents[i].Key, url)
            files[k] = md5sum
          }
          return files
}

func loadLocalFiles(path string) map[string]string {
    files := map[string]string{}
    filepath.Walk(path, func(filePath string, info os.FileInfo, err error) error {
        if !info.IsDir() {
            p := relativePath(path, filePath)

            buf, err := ioutil.ReadFile(p)
            if err != nil {
                panic(err)
            }

            hasher := md5.New()
            hasher.Write(buf)
            md5sum := fmt.Sprintf("%x", hasher.Sum(nil))
            files[p] = md5sum
        }
        return nil
    })
    return files
}

func (s *SyncPair) validPair() bool {
     if validTarget(s.Source) == true && validTarget(s.Target) == true {
         return true
     }
     return false
}

func validTarget(target string) bool {
    // Check for local file
    if pathExists(target) {
        return true
    }

    // Check for valid s3 url
    if validS3Url(target) {
        return true
    }

    return false
}

func validS3Url(path string) bool {
    return strings.HasPrefix(path, "s3://")
}

func pathExists(path string) (bool) {
    _, err := os.Stat(path)
    if err == nil { return true }
    if os.IsNotExist(err) { return false }
    return false
}

func putRoutine(quit chan string, filePath string, bucket *s3.Bucket, file string) {
    Put(bucket, file, filePath)
    quit <- fmt.Sprintf("Completed sync: %s -> s3://%s/%s.", filePath, bucket.Name, file)
}

func getRoutine(quit chan string, filePath string, bucket *s3.Bucket, file string) {
    Get(filePath, bucket, file)
    quit <- fmt.Sprintf("Completed sync: s3://%s/%s -> %s.", bucket.Name, file, filePath)
}

func waitForRoutines(routines []chan string) {
    for _, r := range routines {
        msg := <- r
        fmt.Printf("%s\n", msg)
    }
}

func relativePath(path string, filePath string) string {
    if path == "." {
        return strings.TrimPrefix(filePath, "/")
    } else {
        return strings.TrimPrefix(strings.TrimPrefix(filePath, path), "/")
    }
}
