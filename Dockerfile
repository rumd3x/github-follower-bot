FROM golang

RUN go get github.com/google/go-github/github
RUN go get go.mongodb.org/mongo-driver/mongo
RUN go get github.com/joho/godotenv
RUN go get golang.org/x/oauth2


ADD . /app
WORKDIR /app

RUN go build --buildmode=exe -o bot .

CMD ./bot

