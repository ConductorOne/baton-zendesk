FROM gcr.io/distroless/static-debian11:nonroot
ENTRYPOINT ["/baton-zendesk"]
COPY baton-zendesk /