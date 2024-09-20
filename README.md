# Things I learned in this project

* Browsers API doesn't support additionnal headers on websocket opening. \
  For this specific reason, Kubernetes passes the bearer token via the `Sec-WebSocket-Protocol` header.
* Maps are not thread-safe on reads if a write happens at the same time. 
  More simply, maps are prone to data races: \
  A data race occurs when two goroutines access the same variable concurrently and at least one of the accesses is a write.
  (https://go.dev/doc/articles/race_detector) \
  This means maps reads are thread safe between goroutine if they are read-only.
* go test -race is a neat tool to test goroutine data race issues.
* Use `-trimpath` to remove all
* Opiniated: It is better to unit test the public API rather than the direct implementation. \
  The main reason for that is code refactoring, as a project grows in complexity, refactoring internal implementations would be more difficult and harder to maintain. \
  Nicely explained here: https://www.reddit.com/r/golang/comments/xraka7/comment/iqexb1d \
  "Testing should be a tool we used to ensure that the refactoring we have done did not break current expectations for the public interface. In this way we are free to refactor the implementation too our hearts desire; provided, when we are done, all the tests pass."
