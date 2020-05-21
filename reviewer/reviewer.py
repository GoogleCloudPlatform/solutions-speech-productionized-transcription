# Copyright 2020 Google LLC
#
# Licensed under the Apache License, Version 2.0 (the "License");
# you may not use this file except in compliance with the License.
# You may obtain a copy of the License at
#
# https://www.apache.org/licenses/LICENSE-2.0
#
# Unless required by applicable law or agreed to in writing, software
# distributed under the License is distributed on an "AS IS" BASIS,
# WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
# See the License for the specific language governing permissions and
# limitations under the License.

import argparse
import queue

import eventlet
import redis
from flask import Flask, render_template
from flask_socketio import SocketIO

# for socketio background tasks
eventlet.monkey_patch()

parser = argparse.ArgumentParser()
parser.add_argument('--host', default='localhost')
parser.add_argument('--port', default=8080)
parser.add_argument('--redisHost', required=True)
parser.add_argument('--redisQueue', default='transcriptions')
parser.add_argument('--id', default='Reviewer')
args = parser.parse_args()

health_check_interval = 2
connected_count = 0
buff = queue.Queue()
app = Flask(__name__)
socketio = SocketIO(ping_timeout=5, ping_interval=2)
rdb = redis.Redis(host=args.redisHost, port=6379, db=0, socket_timeout=3,
                  health_check_interval=health_check_interval)


@app.route('/')
def index():
    return render_template('index.html', async_mode=socketio.async_mode)


@socketio.on('connect')
def connect():
    print('Client connected to %s' % args.id)
    socketio.emit('pod_id', args.id)
    buff.put_nowait(1)


@socketio.on('disconnect')
def disconnect():
    print('Client disconnected from %s' % args.id)
    buff.get_nowait()


def _qread():
    while True:
        while not buff.empty():
            try:
                fragment = rdb.brpop(args.redisQueue, timeout=2)
                if fragment is not None:
                    socketio.emit('transcript', fragment[1].decode('utf-8'))
            except redis.exceptions.ReadOnlyError as re:
                print('Redis ReadOnlyError (failover?): %s' % re)
                socketio.emit('transcript', '[REDIS-FAILOVER]')
                # sleep for long enough for health checks to kick in
                socketio.sleep(health_check_interval)
            except redis.exceptions.RedisError as err:
                print('RedisError: %s' % err)
        socketio.sleep(0.2)


if __name__ == '__main__':
    print('Starting %s...' % args.id)
    socketio.init_app(app)
    socketio.start_background_task(_qread)
    socketio.run(app, host=args.host, port=args.port)
