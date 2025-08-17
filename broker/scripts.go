package broker

import "github.com/redis/go-redis/v9"

// اسکریپت برای افزودن پیام به استریم (Task یا RPC)
const scriptEnqueue = `
local stream, msg_type, task_type, payload = KEYS[1], ARGV[1], ARGV[2], ARGV[3]
local reply_to, corr_id = ARGV[4], ARGV[5]

local args = {stream, '*'}
table.insert(args, 'type')
table.insert(args, task_type) -- 'type' field in Redis is the task_type
table.insert(args, 'payload')
table.insert(args, payload)

if reply_to and reply_to ~= '' then
  table.insert(args, 'reply_to')
  table.insert(args, reply_to)
end

if corr_id and corr_id ~= '' then
  table.insert(args, 'correlation_id')
  table.insert(args, corr_id)
end

return redis.call('XADD', unpack(args))
`

// اسکریپت برای انتشار رویداد (Publish)
const scriptPublish = `
return redis.call('PUBLISH', KEYS[1], ARGV[1])
`

var (
	enqueueLua = redis.NewScript(scriptEnqueue)
	publishLua = redis.NewScript(scriptPublish)
)
