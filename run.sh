make
make docker-build docker-push IMG=controllerreg.azurecr.io/wi-acrpull:first
make undeploy
make install
make deploy IMG=controllerreg.azurecr.io/wi-acrpull:first
kubectl apply -f ./config/samples/wi-acrpull_v1_wipullbinding.yaml 