"""Internal-only scoring endpoint (POST /score), authenticated with a
service token. It is never exposed through the public API router and the
compose file publishes no host port for it."""
import hmac
import json
from http.server import BaseHTTPRequestHandler, ThreadingHTTPServer

from .features import build_features


def make_server(host: str, port: int, token: str, model) -> ThreadingHTTPServer:
    class H(BaseHTTPRequestHandler):
        def log_message(self, *a):  # never log request bodies
            pass

        def _send(self, code, obj):
            b = json.dumps(obj).encode()
            self.send_response(code)
            self.send_header("Content-Type", "application/json")
            self.send_header("Content-Length", str(len(b)))
            self.end_headers()
            self.wfile.write(b)

        def do_GET(self):
            if self.path == "/healthz":
                return self._send(200, {"status": "ok", "model_version": model.version})
            self._send(404, {"error": "not found"})

        def do_POST(self):
            if self.path != "/score":
                return self._send(404, {"error": "not found"})
            auth = self.headers.get("Authorization", "")
            if not hmac.compare_digest(auth.encode(), f"Bearer {token}".encode()):
                return self._send(401, {"error": "unauthorized"})
            try:
                n = int(self.headers.get("Content-Length", "0"))
                if n <= 0 or n > 64_000:
                    raise ValueError
                body = json.loads(self.rfile.read(n))
                lead = body.get("lead") or {}
                feats = body.get("features") or build_features(
                    lead, body.get("enrichment"), bool(body.get("matches_open_opportunity_account")))
                score, reasons = model.score(feats)
            except Exception:
                return self._send(400, {"error": "invalid request"})
            self._send(200, {"score": score, "reasons": reasons, "features": feats, "model_version": model.version})

    return ThreadingHTTPServer((host, port), H)
