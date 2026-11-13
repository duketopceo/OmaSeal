.PHONY: build test app clean

build:
	swift build --product OmaSealBar

test:
	swift test

app:
	bash Scripts/bundle-app.sh release

clean:
	swift package clean
	rm -rf dist .build
