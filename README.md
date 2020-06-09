# github-follower-bot
Follows random people exponentially

## Usage

1. Rename the `.env.example` file to `.env` and then edit it, filling the blank fields.

```
ACCESS_TOKEN={{your github access token}}
MONGODB_URI=mongodb://localhost:27017
```

2. Build the included docker image. `docker build -t github-follower-bot .`
3. Start a container with the previously built image it. `docker run -d --name github-follower-bot --restart on-failure github-follower-bot:latest`

The mongodb connection is not optional. It uses database `github` and will insert a new document `{value: "followed_user"}` into the collection named `following` for every successfully followed user.