import http.server, json, sys
from urllib.parse import urlparse, parse_qs

PORT = int(sys.argv[1]) if len(sys.argv) > 1 else 8149

class H(http.server.BaseHTTPRequestHandler):
    def log_message(self, *a):
        pass

    def do_GET(self):
        u = urlparse(self.path)
        q = parse_qs(u.query)
        if u.path.startswith('/v3/movies/'):
            if q.get('api_key', [''])[0] != 'k-test':
                body = b'{"error":"bad key"}'
                self.send_response(401)
            else:
                body = json.dumps({
                    "id": u.path.rsplit('/', 1)[1],
                    "moviebackground": [{"url": "http://127.0.0.1:%d/bg.jpg" % PORT}],
                    "hdmovielogo": [{"url": "http://127.0.0.1:%d/logo.png" % PORT}],
                    "moviethumb": [{"url": "http://127.0.0.1:%d/thumb.jpg" % PORT}],
                }).encode()
                self.send_response(200)
            self.send_header('Content-Type', 'application/json')
            self.send_header('Content-Length', str(len(body)))
            self.end_headers()
            self.wfile.write(body)
        elif u.path == '/bg.jpg':
            data = b'\xff\xd8\xff\xe0' + b'FANARTBG' * 150 + b'\xff\xd9'
            self._img(data)
        elif u.path == '/logo.png':
            data = b'\x89PNG\r\n\x1a\n' + b'FANARTLOGO' * 100
            self._img(data, 'image/png')
        elif u.path == '/thumb.jpg':
            data = b'\xff\xd8\xff\xe0' + b'FANARTTHUMB' * 120 + b'\xff\xd9'
            self._img(data)
        else:
            self.send_response(404)
            self.end_headers()

    def _img(self, data, ct='image/jpeg'):
        self.send_response(200)
        self.send_header('Content-Type', ct)
        self.send_header('Content-Length', str(len(data)))
        self.end_headers()
        self.wfile.write(data)

print('mock fanart on', PORT, flush=True)
http.server.HTTPServer(('127.0.0.1', PORT), H).serve_forever()
