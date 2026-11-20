import http.server, json, sys
from urllib.parse import urlparse, parse_qs

PORT = int(sys.argv[1]) if len(sys.argv) > 1 else 8140

class H(http.server.BaseHTTPRequestHandler):
    def log_message(self, *a):
        pass

    def do_GET(self):
        u = urlparse(self.path)
        if u.path == '/j/subject_suggest':
            q = parse_qs(u.query).get('q', [''])[0]
            body = json.dumps([{
                "title": q or "Douban Movie",
                "year": "2008",
                "img": "http://127.0.0.1:%d/cover.jpg" % PORT,
                "sub_title": "测试",
                "id": "12345",
                "type": "movie",
            }], ensure_ascii=False).encode('utf-8')
            self.send_response(200)
            self.send_header('Content-Type', 'application/json; charset=utf-8')
            self.send_header('Content-Length', str(len(body)))
            self.end_headers()
            self.wfile.write(body)
        elif u.path == '/cover.jpg':
            data = b'\xff\xd8\xff\xe0' + b'DOUBAN' * 150 + b'\xff\xd9'
            self.send_response(200)
            self.send_header('Content-Type', 'image/jpeg')
            self.send_header('Content-Length', str(len(data)))
            self.end_headers()
            self.wfile.write(data)
        else:
            self.send_response(404)
            self.end_headers()

print('mock douban on', PORT, flush=True)
http.server.HTTPServer(('127.0.0.1', PORT), H).serve_forever()
