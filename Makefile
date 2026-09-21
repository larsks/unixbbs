all:
	@printf "Choose one of:\n\n"
	@printf "build -- build new images locally\n"
	@printf "pull -- pull new images\n"
	@printf "refresh -- pull new images and restart containers\n"
	@printf "\n"

pull:
	docker compose --profile pull-only pull

build:
	docker compose -f build.yaml build

refresh: pull
	docker compose up -d
