# Jobico for K8s
JobicoK8S is an open-source platform designed for defining and processing jobs as a collection of events, leveraging the power of Kubernetes and WebAssembly. It enables seamless execution of tasks in a language-agnostic manner, providing a robust infrastructure for complex workflows.

### Key Features

1. **Custom Resource Definitions (CRD) for Job Definitions**
   - **JSON Schema Definition**: Each job is defined using a Custom Resource Definition (CRD) that includes a JSON schema to validate incoming events. This schema ensures that the events conform to the expected structure and content before processing.
   - **WebAssembly Execution**: The CRD also specifies the WebAssembly (Wasm) module responsible for executing the event. This allows for efficient, sandboxed execution of tasks in a variety of programming languages supported by WebAssembly.

2. **Kubernetes Operator**
   - The JobicoK8S Kubernetes Operator automates the creation of the necessary Kubernetes infrastructure for processing events. It monitors the Custom Resources and sets up the required services, deployments, jobs, and configurations to handle the event processing as specified by the CRD.

3. **Core Components**
   - **Listener Component**: JobicoK8S exposes a RESTful API for receiving events. When an event is submitted, it is validated against the JSON schema defined in the job's CRD to ensure it meets all required criteria.
   - **Runner Component**: The runner component retrieves validated events from a queue and executes the associated WebAssembly module. The event data is passed as input arguments to the WebAssembly, allowing for dynamic and flexible processing.

## Architecture

### Kubernetes Operator

![Operator](img/operator.jpg)

### Events processing infraestructure

![Processor](img/processor.jpg)

## Jobs Definition

### Custom Resource Definition (CRD):

The JobicoK8S CRD enables the definition of custom jobs, specifying events executed via WebAssembly (Wasm) files and validated against JSON schemas.

```yaml
apiVersion: jobico.coeux.dev/v1
kind: Job
metadata:
  name: [Unique name for the job resource]
spec:
  events:
    - name: [Descriptive name for the event (used in URI, max 10 chars, alphanumeric)]
      wasm: [Name of the WebAssembly (Wasm) file including its extension]
      schema:
        key: [Key in ConfigMap containing JSON schema for event validation]
```

#### Description

- **apiVersion**: Specifies the version of the API for the Custom Resource Definition (CRD). In this case, `jobico.coeux.dev/v1` indicates the first version (`v1`) of the `jobico` API group.

- **kind**: Indicates the type of resource defined by this CRD. For JobicoK8S, it is `Job`, defining a custom job resource.

- **metadata**:
  - **name**: Specifies a unique name for this job resource. Replace `[Unique name for the job resource]` with a name that uniquely identifies this job definition.

- **spec**:
  - **events**: Defines a list of events associated with the job.
    - **name**: Specifies a descriptive name for the event within the job, which will be used as part of the URI to call the REST interface. Replace `[Descriptive name ...]` with a name that adheres to the constraints (no spaces, alphanumeric characters only, max 10 characters).
    - **wasm**: Specifies the name of the WebAssembly (Wasm) file including its extension, which will execute the logic for this event. Replace `[Name of the WebAssembly ...]` with the actual filename of the Wasm file.
    - **schema**: Specifies the location of the JSON schema definition used to validate incoming events.
      - **key**: Refers to the key within a ConfigMap where the JSON schema for this event is defined. Replace `[Key in ConfigMap ...]` with the actual key name that holds the JSON schema definition.

### ConfigMap for JSON Schema Definition:

```yaml
apiVersion: v1
kind: ConfigMap
metadata:
  name: [Unique identifier for the schema]
data:
  [File name: Schema identifier.json]: |
    [JSON schema content]
```

#### Description

- **apiVersion**: Specifies the API version of Kubernetes used for the ConfigMap resource. In this case, `v1` indicates the core Kubernetes API version.

- **kind**: Defines the type of Kubernetes resource. For a ConfigMap containing a JSON schema, it is `ConfigMap`.

- **metadata**:
  - **name**: Specifies a unique identifier for this ConfigMap resource. Replace `[Unique identifier for the schema]` with a name that uniquely identifies this schema definition.

- **data**:
  - **[File name: Schema identifier.json]**: Defines the filename and identifier for the JSON schema file within the ConfigMap. Replace `[File name: Schema identifier.json]` with the actual filename and identifier for your JSON schema file.
    - **[JSON schema content]**: Contains the actual JSON schema definition. Replace `[JSON schema content]` with the schema content that defines the structure and validation rules for your data.

## Example

```yaml
apiVersion: jobico.coeux.dev/v1
kind: Job
metadata:
  name: job-for-ev1
spec:
  events:
    - name: ev1
      wasm: echo.wasm
      schema:
        key: schema-ev1
---
apiVersion: v1
kind: ConfigMap
metadata:
  name: schema-ev1
data:
  schema-ev1.json: |
    {
      "type": "object",
      "properties": {
          "firstName": {
              "type": "string"
          },
          "lastName": {
              "type": "string"
          },
          "age": {
              "type": "integer"
          }
      },
      "required": ["firstName", "lastName"]
    }
```

### Description

- **Job Definition**:
  - `apiVersion: jobico.coeux.dev/v1` specifies the API version for the Job custom resource.
  - `kind: Job` indicates the type of resource as a Job in JobicoK8S.
  - `metadata: name: job-for-ev1` gives a unique name to the Job resource (`job-for-ev1`).
  - `spec: events:` defines the list of events associated with the job.
    - `- name: ev1` specifies an event named `ev1`, which will be used in the URI for the REST interface.
    - `- wasm: echo.wasm` identifies the WebAssembly (Wasm) file (`echo.wasm`) that will execute the logic for the `ev1` event.
    - `- schema: key: schema-ev1` refers to the ConfigMap key (`schema-ev1`) where the JSON schema for the `ev1` event is defined.

- **ConfigMap (Schema Definition)**:
  - `apiVersion: v1` specifies the Kubernetes API version for the ConfigMap resource.
  - `kind: ConfigMap` indicates the type of Kubernetes resource as a ConfigMap.
  - `metadata: name: schema-ev1` provides a unique name (`schema-ev1`) for the ConfigMap resource containing the schema definition.
  - `data: schema-ev1.json: |` defines the filename (`schema-ev1.json`) and contains the actual JSON schema definition within the ConfigMap.
    - The JSON schema (`{ ... }`) defines an object type with properties like `firstName`, `lastName`, and `age`, with specific data types (`string`, `integer`) and validation rules (`required` fields).

## Getting Started

### Using Jobico-cloud distribution
```bash
# 1- Creates a Kubernetes cluster with 2 nodes
$ git clone https://github.com/andrescosta/jobico-cloud.git
$ cd jobico-cloud
$ ./cluster.sh new

# 2- Compiles and deploys the Kubernetes Operator.
$ ./post/jobicok8s/main.sh .

# 3- Creates a Job which responds to ev1 events
$ cd jobicok8s
$ make ex1

# 4- Sends a simple event for processing
$ ./hacks/test.sh

# 5- Checks the logs
$ kubectl logs -levent=ev1
```
### Using Kind
```bash
# 1- Clone the project
$ git clone https://github.com/andrescosta/jobicok8s.git
$ cd jobicok8s 

# 2- Creates a Kubernetes cluster
$ make -f Makefile.kind kind

# 3- Compiles and deploys the Kubernetes Operator.
$ make deploy-all

# 4- Creates a Job which responds to ev1 events
$ make ex1

# 5- Sends a simple event for processing
$ hacks/test.sh

# 6- Checks the logs
$ kubectl logs -levent=ev1
```
### Prerequisites
- Go version v1.21.0+
- Docker version 17.03+.
- [GNU Make](https://www.gnu.org/software/make/) 
- SSH (only for Jobico-cloud)
- OpenSSL (only for Jobico-cloud)
- [Cloud-init](https://cloud-init.io/) (only for Jobico-cloud)
- [KVM](https://ubuntu.com/blog/kvm-hyphervisor) (only for Jobico-cloud)
- [Helm](https://helm.sh/) (only for Jobico-cloud)