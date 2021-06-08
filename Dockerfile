FROM alpine:edge

RUN apk --update --no-cache add ca-certificates

COPY /discord-twitch-dl /discord-twitch-dl

CMD ["/discord-twitch-dl"]
