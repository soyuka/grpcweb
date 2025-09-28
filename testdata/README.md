# Test build with frankenphp

This demonstrates how to run [FrankenPHP gRPC](https://github.com/dunglas/frankenphp-grpc) with a [gRPC web](https://github.com/grpc/grpc/blob/master/doc/PROTOCOL-WEB.md) support.

To build you will need php-zts to get FrankenPHP then run:

```console
CGO_ENABLED=1 \
    CGO_CFLAGS="$(php-config --includes) -I/home/linuxbrew/.linuxbrew/Cellar/watcher/0.13.8/include/ -I/home/linuxbrew/.linuxbrew/Cellar/php-zts/8.4.12/include/php/" \
    CGO_LDFLAGS="$(php-config --ldflags) $(php-config --libs) -L/home/linuxbrew/.linuxbrew/lib/ -L/usr/lib" \
    go build .
```

Then run the Caddy server:

```console
sudo LD_LIBRARY_PATH=/home/linuxbrew/.linuxbrew/lib:$LD_LIBRARY_PATH ./caddy-grpc-test run
```

Open `https://localhost` and send the hello message to the go grpc server.

## Notes

To build the commonjs Javascript we use:

```console
npx esbuild client.js --bundle --outfile=bundle.js
```

To build the correct web protobuffer files we use:

```console
protoc -I=. helloworld.proto \
        --js_out=import_style=commonjs,binary:. \
        --grpc-web_out=import_style=commonjs,mode=grpcwebtext:.
```

Note that you need `protoc-gen-js` and `protoc-gen-grpc-web`.
