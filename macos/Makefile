.PHONY: build test app clean

build:
	swift build --product OmaSealBar
	swift build --product omaseal-presence

test:
	swift test

app:
	bash Scripts/bundle-app.sh release

clean:
	swift package clean
	rm -rf dist .build
