module my-emby

go 1.21

require github.com/lib/pq v1.12.3

replace github.com/lib/pq => ./third_party/libpq
