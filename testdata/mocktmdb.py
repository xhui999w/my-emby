import http.server, json, sys
from urllib.parse import urlparse

PORT = int(sys.argv[1]) if len(sys.argv) > 1 else 8130

class H(http.server.BaseHTTPRequestHandler):
    def log_message(self, *a):
        pass

    def do_GET(self):
        p = urlparse(self.path).path
        if p.startswith('/3/search/'):
            body = json.dumps({
                "results": [{
                    "id": 603,
                    "title": "The Matrix",
                    "name": "The Matrix",
                    "overview": "TMDB简介：一名黑客发现自己身处虚拟世界。",
                    "poster_path": "/mock.jpg",
                    "backdrop_path": "/mockb.jpg",
                    "release_date": "1999-03-31",
                    "first_air_date": "1999-03-31",
                    "vote_average": 8.2,
                }]
            }).encode('utf-8')
            self.send_response(200)
            self.send_header('Content-Type', 'application/json')
            self.send_header('Content-Length', str(len(body)))
            self.end_headers()
            self.wfile.write(body)
        elif p.startswith('/w500/') or p.startswith('/w780/'):
            data = b'\xff\xd8\xff\xe0' + b'MOCKJPEG' * 200 + b'\xff\xd9'
            self.send_response(200)
            self.send_header('Content-Type', 'image/jpeg')
            self.send_header('Content-Length', str(len(data)))
            self.end_headers()
            self.wfile.write(data)
        else:
            self.send_response(404)
            self.end_headers()

print('mock tmdb on', PORT, flush=True)
http.server.HTTPServer(('127.0.0.1', PORT), H).serve_forever()
