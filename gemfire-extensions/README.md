# gemfire-extensions/: licensed artifacts (not committed)

The examples need two files from the **VMware Tanzu GemFire for gRPC** download. They're
licensed Broadcom artifacts, so they are git-ignored and you supply them yourself.

| File | Used for |
|---|---|
| `gemfire-for-grpc-<version>.gfm` | The extension module, mounted into every locator and server |
| `gemfire.proto` | The service contract; clients generate stubs from it at build time |

## Get them

1. Sign in to the [Broadcom Support Portal](https://support.broadcom.com/group/ecx/productdownloads?subfamily=VMware%20Tanzu%20GemFire%20for%20gRPC)
   and download `gemfire-for-grpc-<version>.tgz`.
2. Extract the two files into this folder:

   ```bash
   tar -xzf gemfire-for-grpc-1.0.0.tgz -C gemfire-extensions/ gemfire-for-grpc-1.0.0.gfm gemfire.proto
   ```

   The result should look like this:

   ```
   gemfire-extensions/
   ├── README.md
   ├── gemfire-for-grpc-1.0.0.gfm
   └── gemfire.proto
   ```

If your version differs, set `GRPC_EXTENSION_FILE` in each example's `.env`.

## The GemFire image

The examples default to `gemfire/gemfire:10.3.2-jdk17`. GemFire for gRPC requires **10.3.2 or later**.
To pull it from Broadcom's registry instead:

```bash
docker login registry.packages.broadcom.com
# then, in an example's .env:
GEMFIRE_IMAGE=registry.packages.broadcom.com/pivotal-gemfire/vmware-gemfire:10.3.2-jdk17
```

The native-client builds copy the GemFire client jars out of this same image, so no Maven
repository credentials are needed.

## Using an example outside this repo

Each example finds this folder through `GEMFIRE_EXTENSIONS_DIR=../gemfire-extensions` in its
`.env`. If you copy an example somewhere else, change `GEMFIRE_EXTENSIONS_DIR` to point here, or to any folder with the same two files.
