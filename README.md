# Filosophy

## How to install
Install all the python packages with
```pip install -r requirements.txt```


```$env:CGO_LDFLAGS = "-L$pwd\native\windows_amd64 -lextractous_ffi -L$pwd\kreuzberg-ffi\lib -lkreuzberg-ffi"```

```
export GOROOT=/mingw64/lib/go
export CGO_ENABLED=1
export CC=x86_64-w64-mingw32-gcc
export CXX=x86_64-w64-mingw32-g++
export PATH=/c/Users/Mahir/filosophy/native/windows_amd64:$PATH
export CGO_LDFLAGS="-L/c/Users/Mahir/filosophy/native/windows_amd64 -lextractous_ffi"
```