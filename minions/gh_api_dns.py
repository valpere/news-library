#!/usr/bin/env python3
"""Opt-in per-command GitHub API DNS workaround; TLS remains end-to-end.
Usage: python3 minions/gh_api_dns.py gh pr list ...
Uses authenticated DNS-over-HTTPS for api.github.com and a loopback CONNECT
proxy restricted to that host/port. No system DNS changes or credential logs.
"""
import ipaddress
import json
import os
import select
import socket
import subprocess
import sys
import threading
import urllib.request
from http.server import BaseHTTPRequestHandler, ThreadingHTTPServer

def main():
    if len(sys.argv)<2:raise SystemExit('provide a command as separate arguments')
    opener=urllib.request.build_opener(urllib.request.ProxyHandler({}))
    with opener.open('https://dns.google/resolve?name=api.github.com&type=A',timeout=10) as response:
        data=json.load(response)
    addresses=[answer['data'] for answer in data.get('Answer',[]) if answer.get('type')==1 and ipaddress.ip_address(answer['data']).is_global]
    if data.get('Status')!=0 or not addresses:raise SystemExit('no public GitHub API address')
    class Proxy(BaseHTTPRequestHandler):
        def log_message(self,*args):pass
        def do_CONNECT(self):
            if self.path.lower()!='api.github.com:443':self.send_error(403);return
            try:upstream=socket.create_connection((addresses[0],443),timeout=10)
            except OSError:self.send_error(502);return
            with upstream:
                self.send_response(200);self.end_headers()
                peers=[self.connection,upstream]
                try:
                    while True:
                        ready,_,_=select.select(peers,[],[],30)
                        if not ready:return
                        for source in ready:
                            payload=source.recv(65536)
                            if not payload:return
                            destination=upstream if source is self.connection else self.connection
                            destination.sendall(payload)
                except OSError:return
    server=ThreadingHTTPServer(('127.0.0.1',0),Proxy)
    server.daemon_threads=True
    thread=threading.Thread(target=server.serve_forever,daemon=True);thread.start()
    env=os.environ.copy()
    env['HTTPS_PROXY']=env['https_proxy']=f'http://127.0.0.1:{server.server_port}'
    env['NO_PROXY']=env['no_proxy']='127.0.0.1,localhost'
    try:result=subprocess.run(sys.argv[1:],env=env)
    finally:server.shutdown();server.server_close();thread.join()
    return result.returncode
if __name__=='__main__':raise SystemExit(main())
