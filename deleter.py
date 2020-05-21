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
import json
import random
import subprocess
import sys
import time
from datetime import datetime

parser = argparse.ArgumentParser()
parser.add_argument('--leader', action='store_true')
parser.add_argument('--applabel', default=None)
parser.add_argument('--count', default=1, type=int)
parser.add_argument('--iterations', default=1, type=int)
parser.add_argument('--delay', default=15, type=int)
args = parser.parse_args()


def delete_leader(cmap='transcriber-lock'):
  """Force-deletes the current transcriber leader pod"""
  config_map_str = _exec_command('kubectl get configmap %s -o json' % cmap)
  config_map = json.loads(config_map_str)
  annotations = config_map['metadata']['annotations']
  leader_props_str = annotations['control-plane.alpha.kubernetes.io/leader']
  leader_props = json.loads(leader_props_str)
  leader_id = leader_props['holderIdentity']
  print('Deleting transcriber leader pod: %s' % leader_id)
  _delete_pod(leader_id)


def delete_pods(app_label=None, count=1):
  """Force-deletes random pod(s), optionally matching label"""
  get_pods_command = 'kubectl get pods -o jsonpath={.items[*].metadata.name}'
  if app_label:
    get_pods_command += ' -l=app=%s' % app_label
  all_pods = _exec_command(get_pods_command)
  if not all_pods:
    print('ERROR: no pods found matching command!')
    sys.exit(1)

  print(all_pods)
  pods = all_pods.split(' ')
  victims = random.sample(pods, min(count, len(pods)))
  print('killing %d pod(s): %s' % (count, victims))
  for victim in victims:
    _delete_pod(victim)


def _delete_pod(name):
  """Force-deletes a single pod"""
  kill_pod_command = 'kubectl delete pod %s --force --grace-period=0' % name
  result = _exec_command(kill_pod_command)
  print('%s at %s' % (result, datetime.utcnow()))


def _exec_command(cmd):
  elems = cmd.split(' ')
  return subprocess.check_output(elems, encoding='utf-8')


for i in range(args.iterations):
  if i > 0:
    time.sleep(args.delay)
  if args.leader:
    delete_leader()
  else:
    delete_pods(args.applabel, args.count)
