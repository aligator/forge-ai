FROM codeberg.org/forgejo/forgejo:15
USER root
RUN apk add --no-cache curl jq su-exec
COPY forgejo-dev.sh /usr/local/bin/forgejo-dev
RUN chmod +x /usr/local/bin/forgejo-dev
ENTRYPOINT ["/usr/local/bin/forgejo-dev"]
