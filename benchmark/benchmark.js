// k6 run --out csv=benchmark_result.csv benchmark.js
import http    from 'k6/http'
import {check} from 'k6'

export const options = {
    stages:     [
        {duration: '30s', target: 1000}, // simulate ramp-up of traffic from 1 to 100 users over 5 minutes.
        {duration: '1m', target: 1000}, // stay at 100 users for 10 minutes
        {duration: '10s', target: 0}, // ramp-down to 0 users
    ],
    thresholds: {
        'http_req_duration':     ['p(99)<1500'], // 99% of requests must complete below 1.5s
        // 'response_successfully': ['p(99)<1500'], // 99% of requests must complete below 1.5s
    },
}


export default function () {
    const res = http.get('http://localhost:9090/forward/items/user')
    check(res, {
        'response successfully': (r) => r.status === 200,
    })
}