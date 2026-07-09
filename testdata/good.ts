import { createStream } from '@starfederation/datastar-sdk'

const stream = createStream({ request, response })

stream.patchElements('<div id="x">x</div>', { selector: '#x' })
stream.removeElement('#x')
