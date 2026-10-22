import http.server
import socket
import threading


class Marker(http.server.BaseHTTPRequestHandler):
    def do_GET(self):
        body = ("PROXIMA_AWS_E2E_OK src=%s\n" % self.client_address[0]).encode()
        self.send_response(200)
        self.send_header("Content-Length", str(len(body)))
        self.end_headers()
        self.wfile.write(body)

    def log_message(self, *args):
        pass


threading.Thread(
    target=http.server.ThreadingHTTPServer(("0.0.0.0", 9090), Marker).serve_forever,
    daemon=True,
).start()

silent = socket.socket()
silent.setsockopt(socket.SOL_SOCKET, socket.SO_REUSEADDR, 1)
silent.bind(("0.0.0.0", 9092))
silent.listen(64)
held = []
while True:
    conn, _ = silent.accept()
    held.append(conn)
