import http.server, json, sys

PORT = int(sys.argv[1]) if len(sys.argv) > 1 else 8191

class H(http.server.BaseHTTPRequestHandler):
    def log_message(self, *a):
        pass
    def do_POST(self):
        n = int(self.headers.get('Content-Length', 0) or 0)
        raw = self.rfile.read(n) if n else b'{}'
        try:
            req = json.loads(raw.decode('utf-8'))
        except Exception:
            req = {}
        lic = str(req.get('license', ''))
        if lic == 'good':
            resp = {"valid": True, "mode": "free", "reason": "", "lease_seconds": 3600}
        else:
            resp = {"valid": False, "mode": "", "reason": "denied", "lease_seconds": 0}
        data = json.dumps(resp).encode()
        self.send_response(200)
        self.send_header('Content-Type', 'application/json')
        self.send_header('Content-Length', str(len(data)))
        self.end_headers()
        self.wfile.write(data)

print('mock license on', PORT, flush=True)
http.server.HTTPServer(('127.0.0.1', PORT), H).serve_forever()
