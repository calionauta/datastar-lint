import { createStream } from '@starfederation/datastar-sdk'

const stream = createStream({ request, response })

stream.patchElements('<div></div>')
stream.removeElement('')
