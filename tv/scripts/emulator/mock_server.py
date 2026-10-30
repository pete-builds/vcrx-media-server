#!/usr/bin/env python3
"""A stand-in VCRX server for the emulator smoke test.

Implements just enough of server/api/openapi.yaml to drive the TV app through
Connect, Login, Home, a library, Details and a refused play request. It insists on
the bearer token like the real server, logs whether each request carried it, and
saves the capabilities the app reported. Standard library only.
"""
import json
import sys
from http.server import BaseHTTPRequestHandler, ThreadingHTTPServer

TOKEN = "smoke-token"
OUT = sys.argv[2] if len(sys.argv) > 2 else "."


def movie(item_id, title, year, position=0, played=False):
    return {
        "id": item_id, "kind": "movie", "library_id": 1, "title": title, "year": year,
        "runtime_ms": 7_200_000, "resolution": "1080p",
        "has_poster": False, "has_backdrop": False, "has_thumb": False, "added_at": 1_761_840_000_000,
        "watch": {"position_ms": position, "duration_ms": 7_200_000, "played": played, "play_count": 1 if played else 0},
    }


MOVIES = [
    movie(101, "The Long Goodbye", 1973, position=2_460_000),
    movie(102, "Paris, Texas", 1984, played=True),
    movie(103, "Stalker", 1979),
    movie(104, "Chungking Express", 1994),
    movie(105, "His Girl Friday", 1940),
    movie(106, "Nosferatu", 1922),
    movie(107, "Plan 9 from Outer Space", 1959),
]
SHOW = {
    "id": 201, "kind": "show", "library_id": 2, "title": "Saltwick Patrol", "year": 2022, "child_count": 2,
    "has_poster": False, "has_backdrop": False, "has_thumb": False, "added_at": 1_761_840_000_000,
    "watch": {"position_ms": 0, "played": False, "play_count": 0, "unplayed_count": 12},
}


def detail(item):
    d = dict(item)
    d.update({
        "overview": "A smoke test item served by tv/scripts/emulator/mock_server.py.",
        "tagline": "Served by the mock.",
        "genres": ["Drama", "Mystery"], "directors": ["A. Director"], "content_rating": "R",
        "ratings": {"imdb": 7.9},
        "versions": [
            {"id": item["id"] * 10, "label": "4K HDR", "file_name": "a.mkv", "container": "mkv", "size": 58_000_000_000,
             "video_codec": "hevc", "resolution": "4K", "hdr_type": "hdr10", "audio_tracks": [], "subtitle_tracks": []},
            {"id": item["id"] * 10 + 1, "label": None, "file_name": "b.mp4", "container": "mp4", "size": 9_000_000_000,
             "video_codec": "h264", "resolution": "1080p", "audio_tracks": [], "subtitle_tracks": []},
        ],
    })
    return d


class Handler(BaseHTTPRequestHandler):
    def log_message(self, fmt, *args):
        auth = "token" if self.headers.get("Authorization") == f"Bearer {TOKEN}" else "no-token"
        sys.stderr.write(f"{self.command} {self.path} [{auth}] {fmt % args}\n")
        sys.stderr.flush()

    def send_json(self, code, body=None):
        data = b"" if body is None else json.dumps(body).encode()
        self.send_response(code)
        if body is not None:
            self.send_header("Content-Type", "application/json")
        self.send_header("Content-Length", str(len(data)))
        self.end_headers()
        self.wfile.write(data)

    def body(self):
        length = int(self.headers.get("Content-Length") or 0)
        return json.loads(self.rfile.read(length) or b"{}")

    def authed(self):
        if self.headers.get("Authorization") == f"Bearer {TOKEN}":
            return True
        self.send_json(401, {"error": "unauthorized", "message": "Please sign in."})
        return False

    def do_GET(self):
        path = self.path.split("?")[0]
        if path == "/api/v1/server":
            return self.send_json(200, {"name": "Smoke test", "product": "vcrx-media-server", "version": "0.0.0-smoke", "api_version": 1})
        if not self.authed():
            return None
        if path == "/api/v1/home":
            return self.send_json(200, {"rows": [
                {"id": "continue_watching", "title": "Continue Watching", "items": [MOVIES[0]]},
                {"id": "recently_added_movies", "title": "Recently Added Movies", "library_id": 1, "items": MOVIES},
                {"id": "library:2", "title": "TV Shows", "library_id": 2, "items": [SHOW]},
            ]})
        if path == "/api/v1/libraries":
            return self.send_json(200, {"libraries": [
                {"id": 1, "name": "Movies", "kind": "movie", "item_count": len(MOVIES)},
                {"id": 2, "name": "TV Shows", "kind": "show", "item_count": 1},
            ]})
        if path == "/api/v1/libraries/1/items":
            return self.send_json(200, {"items": MOVIES, "total": len(MOVIES)})
        if path == "/api/v1/libraries/2/items":
            return self.send_json(200, {"items": [SHOW], "total": 1})
        if path.startswith("/api/v1/items/") and "/artwork/" in path:
            return self.send_json(404, {"error": "not_found", "message": "No artwork."})
        if path.startswith("/api/v1/items/"):
            item_id = int(path.split("/")[4])
            for m in MOVIES + [SHOW]:
                if m["id"] == item_id:
                    return self.send_json(200, detail(m))
        return self.send_json(404, {"error": "not_found", "message": "Not found."})

    def do_POST(self):
        path = self.path.split("?")[0]
        if path == "/api/v1/auth/login":
            body = self.body()
            with open(f"{OUT}/login-request.json", "w") as f:
                json.dump({k: v for k, v in body.items() if k != "password"}, f, indent=2)
            if body.get("username") == "pete" and body.get("password") == "smoke-pass":
                return self.send_json(200, {"token": TOKEN, "expires_at": 0, "user": {"id": 1, "username": "pete", "is_admin": True}})
            return self.send_json(401, {"error": "unauthorized", "message": "Wrong username or password."})
        if not self.authed():
            return None
        if path == "/api/v1/playback/sessions":
            with open(f"{OUT}/capabilities.json", "w") as f:
                json.dump(self.body(), f, indent=2)
            return self.send_json(422, {"error": "transcode_unavailable",
                                        "message": "The smoke test server has no media files to stream."})
        if path.endswith("/events") or path == "/api/v1/auth/logout":
            return self.send_json(204)
        return self.send_json(404, {"error": "not_found", "message": "Not found."})

    def do_PUT(self):
        if self.authed():
            self.send_json(204)


if __name__ == "__main__":
    ThreadingHTTPServer(("127.0.0.1", int(sys.argv[1])), Handler).serve_forever()
