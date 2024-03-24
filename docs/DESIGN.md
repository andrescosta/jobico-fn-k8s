Controllers
-> Job: 
(*) When a new job is creater the controller must:
-> 
Add a new listener for the Job (REST)
Add a new Executor for that Job (WASM)
Add a new 


ListenerJob-> Event schema, Executor(WASM)
QueueJob->Executor(WASM)


Controller->Gets a new Job Definition->if ListenerJob->ReplicaSet for the Listener(schema and resource) and a Job for the executor(event)
				     ->if QueueJob->Job for the executor(event)
Controller->Updates to a Job Definition->Add, Delete or Update
Controller->Delete a Job Definition->Delete


New architecture:

Listener -> Micro http handler->Small http service that listen an event(resource) and validates it against an schema
Queue -> Nats
Executor -> Micro-executor-> Wraps a Jobicolet with a call to the queue and packaged as a container 
Recorder -> (?)
Repository -> (?) check the object storage


Two definitions:
- A Jobicolect cluster:
* "Objects" definitions:
  * Listener (always the same container)
  * Executor (one container per jobicolet)
  * Schema (configmap)
* Services definition:
  * Queue
  * Recorder

Executor packager:
- Compile the 

Executor:
- Load a WASM (resource or directory)
- Gets the NAT URI from env
- Gets the Recorder URI from env

CRD:
----
Events:
    apiVersion: 1
    Kind: events
    spec:
        name: users
        events: 
            - name:my-test
            schema: aaaa
            - name: my-test2
            schema: bbbb

- It transforms to:
  -  a deployment of a well known container (always the same).
  -  a service
  -  an ingress

Jobicolet:
    apiVersion: 1
    Kind: jobicolet
    spec:
        name: users
        executor: 
            - name:my-test
              event: 
              result: 
                ok:
                nok:

- The packager creates the package and uploads the jobicolet to the pod.
- The CRD transforms to:
  - Job
