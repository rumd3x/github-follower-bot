package main

import (
	"context"
	"log"
	"net/http"
	"os"
	"strconv"
	"sync"
	"time"

	"github.com/google/go-github/github"
	"github.com/joho/godotenv"
	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/mongo"
	"go.mongodb.org/mongo-driver/mongo/options"
	"golang.org/x/oauth2"
)

var api *github.Client
var collection *mongo.Collection
var requests chan githubRequest

type githubRequest struct {
	function   string
	parameters []interface{}
	response   chan<- githubResponse
}

type githubResponse struct {
	data interface{}
	err  error
}

func init() {
	err := godotenv.Load()
	if err != nil {
		log.Fatal("Error loading .env file")
	}

	requests = make(chan githubRequest)
	ts := oauth2.StaticTokenSource(
		&oauth2.Token{AccessToken: os.Getenv("ACCESS_TOKEN")},
	)
	tc := oauth2.NewClient(context.Background(), ts)
	api = github.NewClient(tc)

	db, err := mongo.Connect(ctx(10), options.Client().ApplyURI(os.Getenv("MONGODB_URI")))

	if err != nil {
		log.Fatal(err)
	}

	collection = db.Database("github").Collection("following")

	go githubThrottledExecutor()
	log.Println("init done")
}

func main() {
	me := getUser("")
	log.Println("user:", me.GetLogin())

	workers := 5
	var wg sync.WaitGroup
	users := make(chan string)
	for i := 0; i < workers; i++ {
		go followsExecutor(users, &wg)
	}

	for {
		currentRate := getRate()
		log.Println("requests remaining", currentRate.Core.Remaining)
		log.Println("limit resets at", currentRate.Core.Reset)

		following := getAllFollowing(me.GetLogin())
		for _, f := range following {
			wg.Add(1)
			users <- f.GetLogin()
		}

		wg.Wait()
	}
}

func followsExecutor(users <-chan string, wgParent *sync.WaitGroup) {
	for u := range users {
		var wg sync.WaitGroup

		follows := getAllFollowers(u)
		for _, f := range follows {
			user := f.GetLogin()
			if isOnDB(user) {
				continue
			}

			wg.Add(1)
			go followUser(user, &wg)
		}

		wg.Wait()
		wgParent.Done()
	}
}

func followUser(user string, wg *sync.WaitGroup) {
	defer wg.Done()

	if follow(user) {
		log.Println("followed:", user)
		insertDB(user)
	}
}

// github stuff

func getUser(user string) *github.User {
	rchan := make(chan githubResponse)
	requests <- githubRequest{function: "user", parameters: []interface{}{user}, response: rchan}
	response := <-rchan

	if response.err != nil {
		log.Fatalln("fatal error", response.err)
	}
	return response.data.(*github.User)
}

func getAllFollowers(user string) []*github.User {
	result := []*github.User{}
	page := 1
	buffer := getFollowers(user, page)
	for len(buffer) == 100 {
		result = append(result, buffer...)
		page++
		buffer = getFollowing(user, page)
	}
	result = append(result, buffer...)
	return result
}

func getAllFollowing(user string) []*github.User {
	result := []*github.User{}
	page := 1
	buffer := getFollowing(user, page)
	for len(buffer) == 100 {
		result = append(result, buffer...)
		page++
		buffer = getFollowing(user, page)
	}
	result = append(result, buffer...)
	return result
}

func getFollowers(user string, page int) []*github.User {
	rchan := make(chan githubResponse)
	requests <- githubRequest{function: "followers", parameters: []interface{}{user, page}, response: rchan}
	response := <-rchan
	if response.err != nil {
		log.Fatalln("fatal error", response.err)
	}
	return response.data.([]*github.User)
}

func getFollowing(user string, page int) []*github.User {
	rchan := make(chan githubResponse)
	requests <- githubRequest{function: "following", parameters: []interface{}{user, page}, response: rchan}
	response := <-rchan
	if response.err != nil {
		log.Fatalln("fatal error", response.err)
	}
	return response.data.([]*github.User)
}

func follow(user string) bool {
	rchan := make(chan githubResponse)
	requests <- githubRequest{function: "follow", parameters: []interface{}{user}, response: rchan}
	response := <-rchan
	if response.err != nil {
		log.Fatalln("fatal error", response.err)
	}
	return true
}

func getRate() *github.RateLimits {
	rchan := make(chan githubResponse)
	requests <- githubRequest{function: "rate", parameters: nil, response: rchan}
	response := <-rchan
	if response.err != nil {
		log.Fatalln("fatal error", response.err)
	}
	return response.data.(*github.RateLimits)
}

func githubThrottledExecutor() {

	pagination := github.ListOptions{
		PerPage: 100,
		Page:    1,
	}

	for r := range requests {
		switch f := r.function; f {

		case "following":
			pagination.Page = r.parameters[1].(int)
			list, response, err := api.Users.ListFollowing(ctx(5), r.parameters[0].(string), &pagination)
			retry := false
			for handleRateLimit(response, err) {
				retry = true
				list, response, err = api.Users.ListFollowing(ctx(5), r.parameters[0].(string), nil)
			}
			if retry {
				err = nil
			}
			r.response <- githubResponse{data: list, err: err}
			close(r.response)

		case "followers":
			pagination.Page = r.parameters[1].(int)
			list, response, err := api.Users.ListFollowers(ctx(5), r.parameters[0].(string), &pagination)
			retry := false
			for handleRateLimit(response, err) {
				retry = true
				list, response, err = api.Users.ListFollowers(ctx(5), r.parameters[0].(string), nil)
			}
			if retry {
				err = nil
			}
			r.response <- githubResponse{data: list, err: err}
			close(r.response)

		case "user":
			user, response, err := api.Users.Get(ctx(5), r.parameters[0].(string))
			retry := false
			for handleRateLimit(response, err) {
				retry = true
				user, response, err = api.Users.Get(ctx(5), r.parameters[0].(string))
			}
			if retry {
				err = nil
			}
			r.response <- githubResponse{data: user, err: err}
			close(r.response)

		case "rate":
			rate, response, err := api.RateLimits(ctx(5))
			retry := false
			for handleRateLimit(response, err) {
				retry = true
				rate, response, err = api.RateLimits(ctx(5))
			}
			if retry {
				err = nil
			}
			r.response <- githubResponse{data: rate, err: err}
			close(r.response)

		case "follow":
			response, err := api.Users.Follow(ctx(5), r.parameters[0].(string))
			retry := false
			for handleRateLimit(response, err) {
				retry = true
				response, err = api.Users.Follow(ctx(5), r.parameters[0].(string))
			}
			if retry {
				err = nil
			}
			r.response <- githubResponse{data: nil, err: err}
			close(r.response)

		}
	}
}

func handleRateLimit(response *github.Response, err error) bool {

	if rle, ok := err.(*github.RateLimitError); ok {
		log.Println(rle.Message, rle.Rate)

		rate, _, err := api.RateLimits(ctx(5))
		if err != nil {
			log.Fatalln("fatal error", err)
		}

		for rate.Core.Remaining < 1 {
			time.Sleep(1 * time.Minute)
			rate, _, err = api.RateLimits(ctx(5))
			if err != nil {
				log.Fatalln("fatal error", err)
			}
		}

		return true
	}

	if response == nil {
		return false
	}

	if response.StatusCode == http.StatusTooManyRequests {
		log.Println("TooManyRequests", response.Header.Get("Retry-After"))
		retryAfter, err := strconv.ParseInt(response.Header.Get("Retry-After"), 10, 64)
		if err != nil {
			retryAfter = 60
		}
		time.Sleep(time.Duration(retryAfter) * time.Second)

		return true
	}

	if response.StatusCode >= 400 {
		log.Println(response.Status)
		time.Sleep(10 * time.Second)
		return true
	}

	return false
}

// db stuff

func isOnDB(user string) bool {
	var data bson.M
	collection.FindOne(ctx(5), bson.M{"value": user}).Decode(&data)
	return (len(data) > 0)
}

func insertDB(user string) {
	value := bson.M{"value": user}
	collection.InsertOne(ctx(5), value)
}

// utils

func ctx(t time.Duration) context.Context {
	ctx, _ := context.WithTimeout(context.Background(), t*time.Second)
	return ctx
}

func inSlice(slice []int64, val int64) bool {
	for _, item := range slice {
		if item == val {
			return true
		}
	}
	return false
}
