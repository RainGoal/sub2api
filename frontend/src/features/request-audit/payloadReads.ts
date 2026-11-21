// Share the read budget across audit dialogs, including requests finishing after close.
const pending: Array<() => Promise<void>> = []
let active = 0

export function queuePayloadRead(task: () => Promise<void>) {
  pending.push(task)
  drain()
}

function drain() {
  while (active < 2 && pending.length) {
    const task = pending.shift()!
    active++
    const finish = () => { active--; drain() }
    void task().then(finish, finish)
  }
}
